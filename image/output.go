package image

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const outputTooLargeMessage = "image output exceeds the configured per-image limit"

// Output is one validated emitted image: the base64 payload, the sniffed
// MIME, its decoded size, and the SHA-256 fingerprint siblings deduplicate on.
type Output struct {
	Data        string
	MIME        string
	SizeBytes   int64
	Fingerprint string
}

// DecodeOutput validates one native inline image for emission. Output is not
// format-allowlisted: any sniffable raster is emitted with its sniffed MIME,
// but a declared image MIME that disagrees with the bytes is refused.
func DecodeOutput(encoded, declaredMIME string, limit int64) (Output, *OutputError) {
	data, mime, size, failure := DecodeInline(encoded, limit)
	if failure != nil {
		return Output{}, failure
	}

	if declaredMIME != "" && declaredMIME != mime && IsImageMIME(declaredMIME) {
		return Output{}, &OutputError{Reason: ReasonMediaTypeMismatch, Message: "declared media type does not match the image"}
	}

	digest := sha256.Sum256(data)

	return Output{
		Data:        base64.StdEncoding.EncodeToString(data),
		MIME:        mime,
		SizeBytes:   size,
		Fingerprint: hex.EncodeToString(digest[:]),
	}, nil
}

// boundedDecoder retains at most limit bytes while counting the full decoded
// size, so an oversize image is rejected without allocating its whole body.
type boundedDecoder struct {
	data  []byte
	limit int64
	size  int64
}

func (w *boundedDecoder) Write(p []byte) (int, error) {
	w.size += int64(len(p))

	remaining := w.limit - int64(len(w.data))
	if remaining > 0 {
		w.data = append(w.data, p[:min(int64(len(p)), remaining)]...)
	}

	return len(p), nil
}

// DecodeInline decodes native inline base64 output, bounded by limit. It
// returns the retained bytes, the sniffed MIME, and the full decoded size.
func DecodeInline(data string, limit int64) ([]byte, string, int64, *OutputError) {
	decoded := &boundedDecoder{limit: limit + 1}

	if _, err := io.Copy(decoded, base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))); err != nil {
		return nil, "", 0, &OutputError{Reason: ReasonInvalidBase64, Message: "image output contains invalid base64"}
	}

	if decoded.size > limit {
		return nil, "", decoded.size, &OutputError{Reason: ReasonTooLarge, Message: outputTooLargeMessage, SizeBytes: decoded.size, MaxBytes: limit}
	}

	mimeType, ok := sniffMIME(decoded.data)
	if !ok {
		return nil, "", 0, &OutputError{Reason: ReasonNotRaster, Message: "image output bytes are not a raster"}
	}

	return decoded.data, mimeType, decoded.size, nil
}

// ReadFile reads one harness-returned artifact path under the allowed roots,
// bounded by limit, and sniffs its MIME. The path is resolved and compared with
// roots resolved to the same degree.
func ReadFile(path string, roots []string, limit int64) ([]byte, string, *OutputError) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file is missing"}
		}

		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path cannot be resolved safely"}
	}

	if !withinRoots(resolved, roots) {
		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path is outside the allowed roots"}
	}

	// O_NONBLOCK stops a FIFO or a device node swapped in after resolution
	// from parking open(2); the descriptor is inspected once it exists.
	file, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file is missing"}
		}

		return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file cannot be opened"}
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path cannot be inspected safely"}
	}

	if !info.Mode().IsRegular() {
		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path is not a regular file"}
	}

	if info.Size() > limit {
		return nil, "", &OutputError{Reason: ReasonTooLarge, Message: outputTooLargeMessage, SizeBytes: info.Size(), MaxBytes: limit}
	}

	return readContents(file, limit)
}

// readContents reads an already-opened artifact bounded by limit and sniffs its
// MIME.
func readContents(file io.Reader, limit int64) ([]byte, string, *OutputError) {
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file cannot be read"}
	}

	if int64(len(data)) > limit {
		return nil, "", &OutputError{Reason: ReasonTooLarge, Message: outputTooLargeMessage, SizeBytes: int64(len(data)), MaxBytes: limit}
	}

	mimeType, ok := sniffMIME(data)
	if !ok {
		return nil, "", &OutputError{Reason: ReasonNotRaster, Message: "image output file is not a raster"}
	}

	return data, mimeType, nil
}

// withinRoots reports whether an already-resolved path sits under any root.
// Each root is resolved before the lexical comparison.
func withinRoots(resolved string, roots []string) bool {
	for _, root := range roots {
		if withinRoot(resolved, root) {
			return true
		}
	}

	return false
}

func withinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(resolvedRoot, path)
	if err != nil {
		return false
	}

	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
