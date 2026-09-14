package image

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// BlobPolicy decides what a sibling does with an embedded resource blob whose
// normalized MIME is not an image: gate its bytes, or refuse it before decode.
type BlobPolicy func(normalizedMIME string) BlobDisposition

// BlobDisposition is one BlobPolicy answer.
type BlobDisposition int

// The closed blob dispositions.
const (
	// BlobGate decodes the blob, bounds it by the per-image gate, and charges it
	// to the prompt aggregate. The sibling decides afterwards what to forward.
	BlobGate BlobDisposition = iota
	// BlobRefuse rejects the blob before any decode with the uniform unsupported
	// error on prompt.resource.
	BlobRefuse
)

// Options configures one prompt's image validation.
type Options struct {
	Limits Limits
	// HandoffRoot is the configured read root, or empty when handoff is off.
	HandoffRoot string
	// NativeCeiling is an observed native per-image cap below the frame clamp,
	// or zero.
	NativeCeiling int64
	// Blobs decides non-image resource blobs. Nil gates every blob.
	Blobs BlobPolicy
}

// Decoded is one validated image or admitted blob ready for the native request.
// Index counts media blocks; text resources do not consume an index.
type Decoded struct {
	Data  []byte
	MIME  string
	Field string
	Index int
}

// promptMediaKind classifies an inbound block that carries media bytes.
type promptMediaKind int

const (
	mediaImageBlock promptMediaKind = iota
	mediaImageBlob
	mediaOpaqueBlob
	mediaTextResource
)

func (k promptMediaKind) nativeImage() bool { return k == mediaImageBlock || k == mediaImageBlob }

func (k promptMediaKind) perImageBounded() bool { return k != mediaTextResource }

func (k promptMediaKind) field() string {
	if k == mediaImageBlock {
		return FieldPromptImage
	}

	return FieldPromptResource
}

type promptMedia struct {
	kind     promptMediaKind
	data     string
	mimeType string
	uri      string
	meta     map[string]any
}

// ValidatePrompt runs the pinned input gate order over every media-bearing
// block in request order and stops at the first failure. Image and blob blocks
// consume media indexes; text resources only spend the aggregate byte budget.
//
// A refusal and an abort are different answers: the refusal describes a block
// the host can fix, while the error return means the caller stopped waiting.
func ValidatePrompt(ctx context.Context, blocks []acp.ContentBlock, options Options) ([]Decoded, *InputError, error) {
	images := make([]Decoded, 0)

	maxImageBytes := options.Limits.EffectiveInputPerImage(options.NativeCeiling)
	maxPromptBytes := options.Limits.EffectiveInputPerPrompt()

	var (
		promptBytes   int64
		handoffBlocks int64
		index         int
	)

	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		media, ok := promptMediaBlock(block)
		if !ok {
			continue
		}

		if media.kind == mediaOpaqueBlob && options.Blobs != nil && options.Blobs(normalizeMIME(media.mimeType)) == BlobRefuse {
			return nil, &InputError{Code: "unsupported", Field: FieldPromptResource, Index: -1}, nil
		}

		// The count sits behind the unset-root refusal: an adapter with no
		// read root reads nothing, and the root-unset invalid_handoff is how a
		// host learns its root never arrived.
		if options.HandoffRoot != "" && handoffForm(media) {
			handoffBlocks++

			if handoffBlocks > MaxHandoffBlocksPerPrompt {
				return nil, &InputError{
					Code: ErrorTooLarge, Field: media.kind.field(), Index: index,
					SizeBytes: handoffBlocks, MaxBytes: MaxHandoffBlocksPerPrompt,
				}, nil
			}
		}

		data, refusal, err := decodeMedia(ctx, media, index, maxImageBytes, options)
		if err != nil {
			return nil, nil, err
		}

		if refusal != nil {
			return nil, refusal, nil
		}

		size := int64(len(data))
		if media.kind.perImageBounded() && size > maxImageBytes {
			return nil, &InputError{Code: ErrorTooLarge, Field: media.kind.field(), Index: index, SizeBytes: size, MaxBytes: maxImageBytes}, nil
		}

		if media.kind.nativeImage() && options.NativeCeiling > 0 && size > options.NativeCeiling {
			return nil, &InputError{Code: ErrorNativeEnvelope, Field: media.kind.field(), Index: index, SizeBytes: size, MaxBytes: options.NativeCeiling}, nil
		}

		promptBytes += size
		if maxPromptBytes > 0 && promptBytes > maxPromptBytes {
			return nil, &InputError{Code: ErrorTooLarge, Field: media.kind.field(), Index: index, SizeBytes: promptBytes, MaxBytes: maxPromptBytes}, nil
		}

		if media.kind.nativeImage() || media.kind == mediaOpaqueBlob {
			images = append(images, Decoded{Data: data, MIME: media.mimeType, Field: media.kind.field(), Index: index})
		}

		if media.kind != mediaTextResource {
			index++
		}
	}

	return images, nil, nil
}

func handoffForm(media promptMedia) bool {
	return media.kind == mediaImageBlock && media.data == "" && handoffIntent(media)
}

func decodeMedia(ctx context.Context, media promptMedia, index int, maxImageBytes int64, options Options) ([]byte, *InputError, error) {
	switch {
	case media.kind == mediaTextResource:
		return []byte(media.data), nil, nil
	case !media.kind.nativeImage():
		decoded, err := base64.StdEncoding.DecodeString(media.data)
		if err != nil {
			return nil, &InputError{Code: ErrorInvalidBase64, Field: media.kind.field(), Index: index}, nil
		}

		return decoded, nil, nil
	case handoffForm(media):
		return decodeHandoff(ctx, media, index, maxImageBytes, options)
	default:
		data, refusal := decodeEmbedded(media, index)

		return data, refusal, nil
	}
}

func decodeEmbedded(media promptMedia, index int) ([]byte, *InputError) {
	field := media.kind.field()
	if media.data == "" {
		return nil, &InputError{Code: ErrorMissingData, Field: field, Index: index}
	}

	if !slices.Contains(Formats, media.mimeType) {
		return nil, &InputError{Code: ErrorInvalidMediaType, Field: field, Index: index}
	}

	decoded, err := base64.StdEncoding.DecodeString(media.data)
	if err != nil {
		return nil, &InputError{Code: ErrorInvalidBase64, Field: field, Index: index}
	}

	if refusal := checkRaster(decoded, media.mimeType, field, index); refusal != nil {
		return nil, refusal
	}

	return decoded, nil
}

func decodeHandoff(ctx context.Context, media promptMedia, index int, maxImageBytes int64, options Options) ([]byte, *InputError, error) {
	field := media.kind.field()

	data, verdict, err := readHandoff(ctx, options.HandoffRoot, media, maxImageBytes, options.NativeCeiling)
	if err != nil {
		return nil, nil, err
	}

	if verdict != nil {
		return nil, &InputError{
			Code: verdict.code, Field: field, Message: verdict.message, Index: index,
			SizeBytes: verdict.sizeBytes, MaxBytes: verdict.maxBytes,
		}, nil
	}

	if refusal := checkRaster(data, media.mimeType, field, index); refusal != nil {
		return nil, refusal, nil
	}

	return data, nil, nil
}

// checkRaster runs the decode-free structural gates in their pinned order:
// format recognition, dimensions, animation, then declared-versus-sniffed.
func checkRaster(data []byte, mimeType, field string, index int) *InputError {
	raster, err := Inspect(data)

	switch {
	case errors.Is(err, ErrUnknownRaster):
		return &InputError{Code: ErrorMediaTypeMismatch, Field: field, Index: index}
	case err != nil:
		return &InputError{Code: ErrorInvalidDimensions, Field: field, Index: index}
	case raster.Animated:
		return &InputError{Code: ErrorAnimatedUnsupported, Field: field, Index: index}
	case raster.MIME != mimeType:
		return &InputError{Code: ErrorMediaTypeMismatch, Field: field, Index: index}
	default:
		return nil
	}
}

// normalizeMIME lowercases a declared media type and strips parameters for
// routing. Allowlist membership still compares the declared string.
func normalizeMIME(declared string) string {
	value, _, _ := strings.Cut(declared, ";")

	return strings.ToLower(strings.TrimSpace(value))
}

// IsImageMIME reports whether a normalized media type routes to image
// validation.
func IsImageMIME(declared string) bool {
	return strings.HasPrefix(normalizeMIME(declared), "image/")
}

func promptMediaBlock(block acp.ContentBlock) (promptMedia, bool) {
	if block.Image != nil {
		media := promptMedia{kind: mediaImageBlock, data: block.Image.Data, mimeType: block.Image.MimeType, meta: block.Image.Meta}
		if block.Image.Uri != nil {
			media.uri = *block.Image.Uri
		}

		return media, true
	}

	if block.Resource == nil {
		return promptMedia{}, false
	}

	if text := block.Resource.Resource.TextResourceContents; text != nil {
		return promptMedia{kind: mediaTextResource, data: text.Text, uri: text.Uri}, true
	}

	blob := block.Resource.Resource.BlobResourceContents
	if blob == nil {
		return promptMedia{}, false
	}

	declared := ""
	if blob.MimeType != nil {
		declared = *blob.MimeType
	}

	kind := mediaOpaqueBlob
	if IsImageMIME(declared) {
		kind = mediaImageBlob
	}

	return promptMedia{kind: kind, data: blob.Blob, mimeType: declared, uri: blob.Uri}, true
}

// Extension returns the file extension for an allowlisted MIME.
func Extension(mimeType string) string {
	switch mimeType {
	case MIMEPNG:
		return ".png"
	case MIMEJPEG:
		return ".jpg"
	case MIMEGIF:
		return ".gif"
	case MIMEWebP:
		return ".webp"
	default:
		return ""
	}
}
