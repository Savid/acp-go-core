package wire

import (
	"fmt"

	"github.com/savid/acp-go-core/lifecycle"
)

// The four family-global reserved literals.
const (
	MediaEnvelopeKey = "acp-go.dev/mediaEnvelope"
	HandoffKey       = "acp-go.dev/handoff"
	LifecycleKey     = lifecycle.MetaKey
	// CallUsageKey is the usage_update _meta member carrying the token
	// breakdown of the model call the update reports.
	CallUsageKey = "acp-go.dev/callUsage"
)

// ReservedLiterals lists every family-global reserved literal.
var ReservedLiterals = []string{MediaEnvelopeKey, HandoffKey, LifecycleKey, CallUsageKey}

// CheckReservedMeta refuses a host-supplied _meta map carrying any reserved
// literal. Family builders call it before merging caller metadata into a
// request.
func CheckReservedMeta(meta map[string]any) error {
	for _, key := range ReservedLiterals {
		if _, present := meta[key]; present {
			return fmt.Errorf("_meta key %q is a reserved family literal", key)
		}
	}

	return nil
}
