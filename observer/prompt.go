package observer

import (
	"strings"

	"github.com/coder/acp-go-sdk"
)

// PromptResultFrom records the response usage and resolves a provider from a
// qualified model when the caller does not supply one.
func PromptResultFrom(resp acp.PromptResponse, err error, model, provider string) PromptResult {
	result := PromptResult{Err: err, Model: model, Provider: provider, StopReason: string(resp.StopReason)}
	if result.Provider == "" {
		if prefix, _, found := strings.Cut(model, "/"); found {
			result.Provider = prefix
		}
	}

	if resp.Usage != nil {
		result.InputTokens = resp.Usage.InputTokens
		result.OutputTokens = resp.Usage.OutputTokens
		result.TotalTokens = resp.Usage.TotalTokens

		if resp.Usage.CachedReadTokens != nil {
			result.CachedReadTokens = *resp.Usage.CachedReadTokens
		}

		if resp.Usage.CachedWriteTokens != nil {
			result.CachedWriteTokens = *resp.Usage.CachedWriteTokens
		}
	}

	return result
}
