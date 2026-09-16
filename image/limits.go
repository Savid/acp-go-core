// Package image holds the family image gates: the decoded-byte limits, the media
// envelope, prompt image validation in both input forms, and output normalization.
package image

import (
	"fmt"
	"slices"
)

// DefaultLimitBytes is the default decoded-byte bound applied to every Limits
// field: 6 MiB.
const DefaultLimitBytes int64 = 6 * 1024 * 1024

// FrameClamp is the largest decoded image the pinned ACP SDK can carry in one
// JSON-RPC frame. Its base64 form plus 220 bytes of envelope fits the SDK's
// 10,485,760-byte line scanner.
const FrameClamp int64 = 7_864_155

// MaxHandoffBlocksPerPrompt bounds how many handoff-form blocks one prompt may
// read.
const MaxHandoffBlocksPerPrompt = 64

// The inbound media-type allowlist, in the order the media envelope advertises
// it.
const (
	MIMEPNG  = "image/png"
	MIMEJPEG = "image/jpeg"
	MIMEGIF  = "image/gif"
	MIMEWebP = "image/webp"
)

// Formats is the inbound allowlist. The advertisement is a copy of this slice,
// so an accepted format and an advertised format cannot become different sets.
var Formats = []string{MIMEPNG, MIMEJPEG, MIMEGIF, MIMEWebP}

// Limits bounds decoded image bytes accepted from prompts and emitted as output.
// Every field counts decoded bytes. A zero disables that policy limit and never
// bypasses the frame clamp or a native ceiling.
type Limits struct {
	MaxInputBytesPerImage     int64
	MaxInputBytesPerPrompt    int64
	MaxOutputBytesPerImage    int64
	MaxOutputBytesPerToolCall int64
}

// DefaultLimits returns every field at DefaultLimitBytes.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytesPerImage:     DefaultLimitBytes,
		MaxInputBytesPerPrompt:    DefaultLimitBytes,
		MaxOutputBytesPerImage:    DefaultLimitBytes,
		MaxOutputBytesPerToolCall: DefaultLimitBytes,
	}
}

// Validate refuses a negative field.
func (l Limits) Validate() error {
	fields := []struct {
		name  string
		value int64
	}{
		{"MaxInputBytesPerImage", l.MaxInputBytesPerImage},
		{"MaxInputBytesPerPrompt", l.MaxInputBytesPerPrompt},
		{"MaxOutputBytesPerImage", l.MaxOutputBytesPerImage},
		{"MaxOutputBytesPerToolCall", l.MaxOutputBytesPerToolCall},
	}

	for _, field := range fields {
		if field.value < 0 {
			return fmt.Errorf("ImageLimits.%s must be non-negative", field.name)
		}
	}

	return nil
}

// effectiveInputPerImage is the per-image decoded bound the input gates
// enforce: the policy limit clamped to the frame bound and to nativeCeiling
// when one is set. Both the gate and the advertisement read it.
func (l Limits) effectiveInputPerImage(nativeCeiling int64) int64 {
	effective := l.MaxInputBytesPerImage
	if effective <= 0 || effective > FrameClamp {
		effective = FrameClamp
	}

	if nativeCeiling > 0 && nativeCeiling < effective {
		effective = nativeCeiling
	}

	return effective
}

// effectiveInputPerPrompt is the aggregate decoded bound across one prompt. Zero
// bounds no total.
func (l Limits) effectiveInputPerPrompt() int64 { return l.MaxInputBytesPerPrompt }

// EffectiveOutputPerImage clamps the per-image output limit to the frame bound.
func (l Limits) EffectiveOutputPerImage() int64 { return clampOutput(l.MaxOutputBytesPerImage) }

// EffectiveOutputPerToolCall clamps the per-tool-call output limit to the frame
// bound.
func (l Limits) EffectiveOutputPerToolCall() int64 { return clampOutput(l.MaxOutputBytesPerToolCall) }

func clampOutput(configured int64) int64 {
	if configured <= 0 || configured > FrameClamp {
		return FrameClamp
	}

	return configured
}

// Envelope describes the media envelope inputs that vary per sibling.
type Envelope struct {
	// NativeCeiling is an observed native per-image byte cap below the frame
	// clamp, or zero.
	NativeCeiling int64
	// MaxDimension is an enforced native per-dimension pixel bound, or zero.
	MaxDimension int64
	// DocumentFormats lists MIMEs the sibling maps to a native document
	// representation.
	DocumentFormats []string
}

// MediaEnvelope renders the acp-go.dev/mediaEnvelope advertisement from the
// same effective limits the gates enforce.
func MediaEnvelope(limits Limits, envelope Envelope) map[string]any {
	documents := envelope.DocumentFormats
	if documents == nil {
		documents = []string{}
	}

	return map[string]any{
		"maxBytes":        limits.effectiveInputPerImage(envelope.NativeCeiling),
		"maxPromptBytes":  limits.effectiveInputPerPrompt(),
		"maxDimension":    envelope.MaxDimension,
		"imageFormats":    slices.Clone(Formats),
		"documentFormats": slices.Clone(documents),
	}
}
