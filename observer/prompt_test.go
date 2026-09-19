package observer

import (
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestPromptResultFromPreservesUsageAndFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("turn failed")
	cachedRead, cachedWrite := 4, 3
	resp := acp.PromptResponse{StopReason: acp.StopReasonEndTurn, Usage: &acp.Usage{
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		CachedReadTokens: &cachedRead, CachedWriteTokens: &cachedWrite,
	}}
	result := PromptResultFrom(resp, failure, "provider/model", "")
	require.Equal(t, PromptResult{Err: failure, Model: "provider/model", Provider: "provider", StopReason: "end_turn",
		InputTokens: 10, OutputTokens: 5, TotalTokens: 15, CachedReadTokens: 4, CachedWriteTokens: 3}, result)
	require.Equal(t, "openai", PromptResultFrom(acp.PromptResponse{}, nil, "model", "openai").Provider)
	require.Empty(t, PromptResultFrom(acp.PromptResponse{}, nil, "model", "").Provider)
}
