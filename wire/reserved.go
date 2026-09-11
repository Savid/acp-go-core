package wire

import "fmt"

// The three family-global reserved literals.
const (
	MediaEnvelopeKey = "acp-go.dev/mediaEnvelope"
	HandoffKey       = "acp-go.dev/handoff"
	LifecycleKey     = "acp-go.dev/lifecycle"
)

// ReservedLiterals lists every acp-go.dev/* key.
var ReservedLiterals = []string{MediaEnvelopeKey, HandoffKey, LifecycleKey}

// ReservedKeyError reports a host-supplied _meta map that carries a family
// literal.
type ReservedKeyError struct {
	Key string
}

func (e *ReservedKeyError) Error() string {
	return fmt.Sprintf("_meta key %q is a reserved family literal", e.Key)
}

// CheckReservedMeta refuses a host-supplied _meta map carrying any reserved
// literal. Family builders call it before merging caller metadata into a
// request.
func CheckReservedMeta(meta map[string]any) error {
	for _, key := range ReservedLiterals {
		if _, present := meta[key]; present {
			return &ReservedKeyError{Key: key}
		}
	}

	return nil
}
