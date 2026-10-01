package wire

// CallUsage is one model call's token breakdown as the harness reported it. A
// nil member is a figure the harness did not report for the call.
type CallUsage struct {
	// ResponseID is the id the model gateway returned for the call's response,
	// empty when the harness does not expose it.
	ResponseID string `json:"responseId,omitempty"`
	// InputTokens is the input the call sent that was neither read from nor
	// written to a prompt cache.
	InputTokens *int `json:"inputTokens,omitempty"`
	// CachedReadTokens is the input the call read from a prompt cache.
	CachedReadTokens *int `json:"cachedReadTokens,omitempty"`
	// CachedWriteTokens is the input the call wrote to a prompt cache.
	CachedWriteTokens *int `json:"cachedWriteTokens,omitempty"`
	// OutputTokens is what the call generated, reasoning included.
	OutputTokens *int `json:"outputTokens,omitempty"`
}

// Known reports whether the call reported any token; the response id alone
// states no usage. A report whose every token member is absent or zero states
// nothing: a model call never has an empty context, and a gateway replaying a
// cached response reports one that way.
func (u CallUsage) Known() bool {
	for _, member := range []*int{u.InputTokens, u.CachedReadTokens, u.CachedWriteTokens, u.OutputTokens} {
		if member != nil && *member != 0 {
			return true
		}
	}

	return false
}

// Apply writes the member onto a usage_update's meta, allocating it when nil,
// and returns the map. A breakdown that is not Known writes nothing.
func (u CallUsage) Apply(meta map[string]any) map[string]any {
	if !u.Known() {
		return meta
	}

	if meta == nil {
		meta = make(map[string]any, 1)
	}

	meta[CallUsageKey] = u

	return meta
}
