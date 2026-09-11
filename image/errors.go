package image

import (
	"github.com/coder/acp-go-sdk"

	"github.com/savid/acp-go-core/wire"
)

// Input block fields an input refusal names.
const (
	FieldPromptImage    = "prompt.image"
	FieldPromptResource = "prompt.resource"
)

// The closed input error vocabulary.
const (
	ErrorMissingData         = "missing_data"
	ErrorInvalidBase64       = "invalid_base64"
	ErrorInvalidMediaType    = "invalid_media_type"
	ErrorMediaTypeMismatch   = "media_type_mismatch"
	ErrorAnimatedUnsupported = "animated_not_supported"
	ErrorInvalidDimensions   = "invalid_dimensions"
	ErrorTooLarge            = "too_large"
	ErrorUnsupportedByModel  = "unsupported_by_model"
	ErrorNativeEnvelope      = "native_envelope_exceeded"
	ErrorInvalidHandoff      = "invalid_handoff"
	ErrorPathNotAllowed      = "path_not_allowed"
	ErrorMissingFile         = "missing_file"
	ErrorDigestMismatch      = "handoff_digest_mismatch"
)

// InputError is one input gate's refusal. Field names the inbound block type;
// Index counts every block that entered gated-media validation.
type InputError struct {
	Code      string
	Field     string
	Message   string
	Index     int
	SizeBytes int64
	MaxBytes  int64
}

func (e *InputError) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}

	return e.Code
}

// InvalidParams renders the refusal as the uniform -32602 image data.
func (e *InputError) InvalidParams() *acp.RequestError {
	data := map[string]any{
		wire.FieldField: e.Field,
		wire.FieldError: e.Code,
		"index":         e.Index,
	}
	if e.Message != "" {
		data[wire.FieldMessage] = e.Message
	}

	if e.SizeBytes > 0 || e.Code == ErrorTooLarge {
		data["sizeBytes"] = e.SizeBytes
	}

	if e.MaxBytes > 0 || e.Code == ErrorTooLarge {
		data["maxBytes"] = e.MaxBytes
	}

	return acp.NewInvalidParams(data)
}

// UnsupportedByModel builds the prompt-time refusal for a text-only selected
// model, naming the first gated-media block.
func UnsupportedByModel(field string, index int) *InputError {
	return &InputError{Code: ErrorUnsupportedByModel, Field: field, Index: index}
}

// OutputStage is the turn-failure stage for image output.
const OutputStage = "image_output"

// The closed output reason vocabulary.
const (
	ReasonInvalidBase64     = "invalid_base64"
	ReasonNotRaster         = "not_a_raster"
	ReasonMediaTypeMismatch = "media_type_mismatch"
	ReasonMissingFile       = "missing_file"
	ReasonPathNotAllowed    = "path_not_allowed"
	ReasonTooLarge          = "too_large"
	ReasonStorageFailed     = "storage_failed"
)

// Guidance carried back to the model when an output is refused in place. Each
// string is a constant keyed only by the reason and interpolates nothing about
// the artifact.
const (
	GuidancePathNotAllowed    = "write the image inside the workspace and try again"
	GuidanceMissingFile       = "the image file could not be read; write the image inside the workspace and try again"
	GuidanceTooLarge          = "the image is too large to send; write a smaller image and try again"
	GuidanceNotRaster         = "the file is not a supported raster image; write a PNG, JPEG, GIF, WebP, BMP, ICO, or TIFF and try again"
	GuidanceInvalidBase64     = "the image payload could not be decoded; write the image to a file inside the workspace and try again"
	GuidanceMediaTypeMismatch = "the declared media type does not match the image; write the image again with a matching media type"
)

// OutputError is one output verdict.
type OutputError struct {
	Reason    string
	Message   string
	SizeBytes int64
	MaxBytes  int64
}

func (e *OutputError) Error() string { return e.Message }

// Guidance returns the constant text a recoverable verdict reports to the model
// and whether the verdict is recoverable. Only storage_failed is not.
func (e *OutputError) Guidance() (string, bool) {
	switch e.Reason {
	case ReasonPathNotAllowed:
		return GuidancePathNotAllowed, true
	case ReasonMissingFile:
		return GuidanceMissingFile, true
	case ReasonTooLarge:
		return GuidanceTooLarge, true
	case ReasonNotRaster:
		return GuidanceNotRaster, true
	case ReasonInvalidBase64:
		return GuidanceInvalidBase64, true
	case ReasonMediaTypeMismatch:
		return GuidanceMediaTypeMismatch, true
	default:
		return "", false
	}
}

// TurnFailure renders a fatal output verdict as the turn-failure envelope
// members.
func (e *OutputError) TurnFailure() wire.TurnFailure {
	return wire.TurnFailure{
		Cause:     wire.CauseTransport,
		Message:   e.Message,
		Stage:     OutputStage,
		Reason:    e.Reason,
		SizeBytes: e.SizeBytes,
		MaxBytes:  e.MaxBytes,
	}
}
