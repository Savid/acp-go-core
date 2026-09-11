package image

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const outputTooLargeMessage = "image output exceeds the configured per-image limit"

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

	mimeType, ok := SniffMIME(decoded.data)
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

	if !WithinRoots(resolved, roots) {
		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path is outside the allowed roots"}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file is missing"}
		}

		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path cannot be inspected safely"}
	}

	if !info.Mode().IsRegular() {
		return nil, "", &OutputError{Reason: ReasonPathNotAllowed, Message: "image output path is not a regular file"}
	}

	if info.Size() > limit {
		return nil, "", &OutputError{Reason: ReasonTooLarge, Message: outputTooLargeMessage, SizeBytes: info.Size(), MaxBytes: limit}
	}

	file, err := os.Open(resolved)
	if err != nil {
		return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file cannot be opened"}
	}

	defer func() { _ = file.Close() }()

	return ReadContents(file, limit)
}

// ReadContents reads an already-opened artifact bounded by limit and sniffs its
// MIME.
func ReadContents(file io.Reader, limit int64) ([]byte, string, *OutputError) {
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "", &OutputError{Reason: ReasonMissingFile, Message: "image output file cannot be read"}
	}

	if int64(len(data)) > limit {
		return nil, "", &OutputError{Reason: ReasonTooLarge, Message: outputTooLargeMessage, SizeBytes: int64(len(data)), MaxBytes: limit}
	}

	mimeType, ok := SniffMIME(data)
	if !ok {
		return nil, "", &OutputError{Reason: ReasonNotRaster, Message: "image output file is not a raster"}
	}

	return data, mimeType, nil
}

// WithinRoots reports whether an already-resolved path sits under any root.
// Each root is resolved before the lexical comparison.
func WithinRoots(resolved string, roots []string) bool {
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
