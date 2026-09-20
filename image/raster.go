package image

import (
	"encoding/binary"
	"errors"
	"net/http"
)

// raster is what a decode-free header walk learns about an image.
type raster struct {
	MIME     string
	Animated bool
}

// errUnknownRaster reports bytes whose magic matches no allowlisted raster; a
// recognized header with no valid dimensions returns a plain error.
var errUnknownRaster = errors.New("bytes do not sniff as a known raster format")

// inspect walks the header of an allowlisted raster without decoding it and
// allocates nothing proportional to the declared size.
func inspect(data []byte) (raster, error) {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return inspectPNG(data)
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return inspectJPEG(data)
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return inspectGIF(data)
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return inspectWebP(data)
	default:
		return raster{}, errUnknownRaster
	}
}

// sniffMIME reports the truthful media type of an output artifact. Output is not
// allowlisted: any sniffable raster is emitted with its sniffed type.
func sniffMIME(data []byte) (string, bool) {
	if info, err := inspect(data); err == nil {
		return info.MIME, true
	}

	switch detected := http.DetectContentType(data); detected {
	case "image/bmp", "image/x-icon", "image/vnd.microsoft.icon", "image/tiff":
		return detected, true
	}

	if len(data) >= 4 && (string(data[:4]) == "II*\x00" || string(data[:4]) == "MM\x00*") {
		return "image/tiff", true
	}

	return "", false
}

func inspectPNG(data []byte) (raster, error) {
	if len(data) < 33 || string(data[12:16]) != "IHDR" || binary.BigEndian.Uint32(data[8:12]) != 13 {
		return raster{}, errors.New("invalid PNG dimensions")
	}

	width := int(binary.BigEndian.Uint32(data[16:20]))

	height := int(binary.BigEndian.Uint32(data[20:24]))
	if width <= 0 || height <= 0 {
		return raster{}, errors.New("invalid PNG dimensions")
	}

	animated := false

	for offset := 8; offset+12 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length < 0 || offset+12+length > len(data) {
			break
		}

		chunkType := string(data[offset+4 : offset+8])
		if chunkType == "acTL" {
			animated = true

			break
		}

		if chunkType == "IDAT" || chunkType == "IEND" {
			break
		}

		offset += 12 + length
	}

	return raster{MIME: MIMEPNG, Animated: animated}, nil
}

func inspectJPEG(data []byte) (raster, error) {
	for offset := 2; offset+1 < len(data); {
		if data[offset] != 0xff {
			offset++

			continue
		}

		for offset < len(data) && data[offset] == 0xff {
			offset++
		}

		if offset >= len(data) {
			break
		}

		marker := data[offset]
		offset++

		if marker == 0xd8 || marker == 0xd9 || marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}

		if offset+2 > len(data) {
			break
		}

		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			break
		}

		if jpegSOFMarker(marker) && length >= 7 {
			height := int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))

			width := int(binary.BigEndian.Uint16(data[offset+5 : offset+7]))
			if width <= 0 || height <= 0 {
				break
			}

			return raster{MIME: MIMEJPEG}, nil
		}

		offset += length
	}

	return raster{}, errors.New("invalid JPEG dimensions")
}

func jpegSOFMarker(marker byte) bool {
	switch marker {
	case 0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf:
		return true
	default:
		return false
	}
}

func inspectGIF(data []byte) (raster, error) {
	if len(data) < 13 {
		return raster{}, errors.New("invalid GIF dimensions")
	}

	width := int(binary.LittleEndian.Uint16(data[6:8]))

	height := int(binary.LittleEndian.Uint16(data[8:10]))
	if width <= 0 || height <= 0 {
		return raster{}, errors.New("invalid GIF dimensions")
	}

	still := raster{MIME: MIMEGIF}

	offset := 13
	if data[10]&0x80 != 0 {
		offset += 3 * (1 << ((data[10] & 0x07) + 1))
	}

	images := 0

	for offset < len(data) {
		switch data[offset] {
		case 0x2c:
			images++

			if images > 1 {
				still.Animated = true

				return still, nil
			}

			if offset+10 > len(data) {
				return still, nil
			}

			packed := data[offset+9]

			offset += 10
			if packed&0x80 != 0 {
				offset += 3 * (1 << ((packed & 0x07) + 1))
			}

			if offset >= len(data) {
				return still, nil
			}

			offset++
			offset = skipGIFSubBlocks(data, offset)
		case 0x21:
			if offset+2 > len(data) {
				return still, nil
			}

			offset = skipGIFSubBlocks(data, offset+2)
		default:
			return still, nil
		}
	}

	return still, nil
}

func skipGIFSubBlocks(data []byte, offset int) int {
	for offset < len(data) {
		size := int(data[offset])
		offset++

		if size == 0 {
			break
		}

		if offset+size > len(data) {
			return len(data)
		}

		offset += size
	}

	return offset
}

func inspectWebP(data []byte) (raster, error) {
	for offset := 12; offset+8 <= len(data); {
		chunkType := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))

		payload := offset + 8
		if size < 0 || payload+size > len(data) {
			break
		}

		switch chunkType {
		case "VP8X":
			if size < 10 {
				return raster{}, errors.New("invalid WebP dimensions")
			}

			return raster{MIME: MIMEWebP, Animated: data[payload]&0x02 != 0}, nil
		case "VP8 ":
			if size >= 10 && data[payload+3] == 0x9d && data[payload+4] == 0x01 && data[payload+5] == 0x2a {
				width := int(binary.LittleEndian.Uint16(data[payload+6:payload+8]) & 0x3fff)

				height := int(binary.LittleEndian.Uint16(data[payload+8:payload+10]) & 0x3fff)
				if width > 0 && height > 0 {
					return raster{MIME: MIMEWebP}, nil
				}
			}
		case "VP8L":
			if size >= 5 && data[payload] == 0x2f {
				return raster{MIME: MIMEWebP}, nil
			}
		}

		offset = payload + size
		if size%2 != 0 {
			offset++
		}
	}

	return raster{}, errors.New("invalid WebP dimensions")
}
