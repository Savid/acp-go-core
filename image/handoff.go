package image

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/savid/acp-go-core/wire"
)

const (
	handoffVersion      = 1
	handoffVersionKey   = "version"
	handoffDigestKey    = "digest"
	handoffSizeBytesKey = "sizeBytes"
	handoffURIScheme    = "file"
	handoffLocalhost    = "localhost"
	handoffFields       = 3
	handoffDigestLength = sha256.Size * 2
	handoffParentName   = ".."

	// handoffNumberCeiling is 2^63 as a float64: the first value an int64
	// cannot hold.
	handoffNumberCeiling = 9223372036854775808.0

	// handoffOpenFlags stop a FIFO or a device node from parking open(2) in the
	// kernel. A confined root refuses anything outside it but deliberately does
	// not refuse a device or a pipe, so the descriptor is checked once it exists.
	handoffOpenFlags = syscall.O_NONBLOCK
)

// Every client-visible handoff message is one of these constants. A refusal
// names the stage that refused and nothing the caller could not already state.
const (
	handoffCauseRootUnset         = "no input handoff root is configured"
	handoffCauseRootUnopenable    = "configured handoff root cannot be opened"
	handoffCauseEnvelopeMissing   = "_meta." + wire.HandoffKey + " is required"
	handoffCauseEnvelopeNotObject = "_meta." + wire.HandoffKey + " must be an object"
	handoffCauseEnvelopeFields    = "_meta." + wire.HandoffKey + " must contain exactly version, digest, and sizeBytes"
	handoffCauseEnvelopeVersion   = "_meta." + wire.HandoffKey + ".version must be the supported handoff version"
	handoffCauseEnvelopeDigest    = "_meta." + wire.HandoffKey + ".digest must be a lowercase hex sha256"
	handoffCauseEnvelopeSizeBytes = "_meta." + wire.HandoffKey + ".sizeBytes must be a non-negative integer"
	handoffCauseURIMissing        = "uri is required"
	handoffCauseURIUnparsable     = "uri is not parseable"
	handoffCauseURIScheme         = "uri scheme is not " + handoffURIScheme
	handoffCauseURIHost           = "uri host is not this host"
	handoffCauseURIRelative       = "uri path is not absolute"
	handoffCauseOutsideRoot       = "path cannot be opened inside the configured handoff root"
	handoffCauseNotRegular        = "path is not a regular file"
	handoffCauseMissing           = "path does not exist"
	handoffCauseUnreadable        = "path cannot be read"
	handoffCauseDigestMismatch    = "handoff file does not match the declared envelope"
)

// HandoffAdvertisement is the agentCapabilities._meta value advertised while a
// handoff root is configured.
func HandoffAdvertisement() map[string]any {
	return map[string]any{handoffVersionKey: handoffVersion}
}

// ValidateHandoffRoot refuses a relative root at construction.
func ValidateHandoffRoot(dir string) error {
	if dir == "" || filepath.IsAbs(dir) {
		return nil
	}

	return errors.New("InputHandoffRoot must be an absolute path, got " + dir)
}

type handoffEnvelope struct {
	digest    string
	sizeBytes int64
}

type handoffVerdict struct {
	code      string
	message   string
	sizeBytes int64
	maxBytes  int64
}

// handoffIntent reports whether a block asked for the handoff transport: a
// handoff envelope, or a file URI.
func handoffIntent(media promptMedia) bool {
	if _, declared := media.meta[wire.HandoffKey]; declared {
		return true
	}

	parsed, err := url.Parse(media.uri)

	return err == nil && parsed.Scheme == handoffURIScheme
}

// readHandoff resolves, reads, and digest-verifies one handoff-form block. Every
// verdict the request decides on its own comes first and opens nothing; the
// filesystem answers the rest. The read is bounded by the declared size plus
// one byte, so a file that differs from its declaration fails verification.
func readHandoff(ctx context.Context, root string, media promptMedia, maxBytes int64) ([]byte, *handoffVerdict, error) {
	if root == "" {
		return nil, &handoffVerdict{code: ErrorInvalidHandoff, message: handoffCauseRootUnset}, nil
	}

	envelope, message := parseHandoffEnvelope(media.meta)
	if message != "" {
		return nil, &handoffVerdict{code: ErrorInvalidHandoff, message: message}, nil
	}

	path, message := handoffURIPath(media.uri)
	if message != "" {
		return nil, &handoffVerdict{code: ErrorInvalidHandoff, message: message}, nil
	}

	if !slices.Contains(formats, media.mimeType) {
		return nil, &handoffVerdict{code: ErrorInvalidMediaType}, nil
	}

	if envelope.sizeBytes > maxBytes {
		return nil, &handoffVerdict{code: ErrorTooLarge, sizeBytes: envelope.sizeBytes, maxBytes: maxBytes}, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	file, verdict := openHandoff(root, path)
	if verdict != nil {
		return nil, verdict, nil
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, envelope.sizeBytes+1))
	if err != nil {
		return nil, &handoffVerdict{code: ErrorMissingFile, message: handoffCauseUnreadable}, nil
	}

	if !handoffBytesMatch(data, envelope) {
		return nil, &handoffVerdict{code: ErrorDigestMismatch, message: handoffCauseDigestMismatch}, nil
	}

	return data, nil, nil
}

// openHandoff opens one host-named path confined to the root. Confinement is
// the kernel's answer to the open, so nothing can change between deciding a
// path is admissible and reading it; the descriptor is then required to be a
// regular file.
func openHandoff(root, path string) (io.ReadCloser, *handoffVerdict) {
	confined, err := os.OpenRoot(root)
	if err != nil {
		return nil, &handoffVerdict{code: ErrorPathNotAllowed, message: handoffCauseRootUnopenable}
	}

	defer func() { _ = confined.Close() }()

	file, err := confined.OpenFile(handoffRelativeName(root, path), os.O_RDONLY|handoffOpenFlags, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &handoffVerdict{code: ErrorMissingFile, message: handoffCauseMissing}
		}

		return nil, &handoffVerdict{code: ErrorPathNotAllowed, message: handoffCauseOutsideRoot}
	}

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()

		return nil, &handoffVerdict{code: ErrorPathNotAllowed, message: handoffCauseNotRegular}
	}

	return file, nil
}

// handoffRelativeName expresses the block's path relative to the root, resolving
// directory aliases when the spellings differ. The final component stays
// unresolved so the root owns its symlink and file-type checks.
func handoffRelativeName(dir, path string) string {
	if relative, err := filepath.Rel(dir, path); err == nil && filepath.IsLocal(relative) {
		return relative
	}

	resolvedRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return handoffParentName
	}

	// The deepest existing ancestor is resolved; the missing remainder is
	// re-appended so the root's open reports it missing, not escaping.
	parent, remainder := filepath.Dir(path), filepath.Base(path)

	for {
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			relative, relErr := filepath.Rel(resolvedRoot, filepath.Join(resolvedParent, remainder))
			if relErr != nil {
				return handoffParentName
			}

			return relative
		}

		if !errors.Is(err, fs.ErrNotExist) || parent == filepath.Dir(parent) {
			return handoffParentName
		}

		remainder = filepath.Join(filepath.Base(parent), remainder)
		parent = filepath.Dir(parent)
	}
}

func parseHandoffEnvelope(meta map[string]any) (handoffEnvelope, string) {
	raw, declared := meta[wire.HandoffKey]
	if !declared {
		return handoffEnvelope{}, handoffCauseEnvelopeMissing
	}

	value, ok := raw.(map[string]any)
	if !ok {
		return handoffEnvelope{}, handoffCauseEnvelopeNotObject
	}

	if len(value) != handoffFields {
		return handoffEnvelope{}, handoffCauseEnvelopeFields
	}

	version, ok := handoffNumber(value[handoffVersionKey])
	if !ok || version != handoffVersion {
		return handoffEnvelope{}, handoffCauseEnvelopeVersion
	}

	digest, ok := value[handoffDigestKey].(string)
	if !ok || !handoffDigest(digest) {
		return handoffEnvelope{}, handoffCauseEnvelopeDigest
	}

	sizeBytes, ok := handoffNumber(value[handoffSizeBytesKey])
	if !ok || sizeBytes < 0 {
		return handoffEnvelope{}, handoffCauseEnvelopeSizeBytes
	}

	return handoffEnvelope{digest: digest, sizeBytes: sizeBytes}, ""
}

func handoffNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		number, err := typed.Int64()

		return number, err == nil
	case float64:
		if typed != math.Trunc(typed) || typed < 0 || typed >= handoffNumberCeiling {
			return 0, false
		}

		return int64(typed), true
	default:
		return 0, false
	}
}

func handoffDigest(digest string) bool {
	if len(digest) != handoffDigestLength {
		return false
	}

	for index := range len(digest) {
		char := digest[index]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}

	return true
}

func handoffURIPath(uri string) (string, string) {
	if uri == "" {
		return "", handoffCauseURIMissing
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return "", handoffCauseURIUnparsable
	}

	if parsed.Scheme != handoffURIScheme {
		return "", handoffCauseURIScheme
	}

	if parsed.Host != "" && parsed.Host != handoffLocalhost {
		return "", handoffCauseURIHost
	}

	path := filepath.FromSlash(parsed.Path)
	if !filepath.IsAbs(path) {
		return "", handoffCauseURIRelative
	}

	return path, ""
}

func handoffBytesMatch(data []byte, envelope handoffEnvelope) bool {
	if int64(len(data)) != envelope.sizeBytes {
		return false
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	return subtle.ConstantTimeCompare([]byte(digest), []byte(envelope.digest)) == 1
}
