package wire

import (
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestCallUsageKnownNeedsANonZeroFigure(t *testing.T) {
	t.Parallel()

	require.False(t, CallUsage{}.Known())
	require.False(t, CallUsage{InputTokens: new(0), CachedReadTokens: new(0), CachedWriteTokens: new(0), OutputTokens: new(0)}.Known())
	require.True(t, CallUsage{CachedReadTokens: new(12)}.Known())
	require.True(t, CallUsage{InputTokens: new(0), OutputTokens: new(3)}.Known())
}

func TestCallUsageApplyWritesOnlyReportedMembers(t *testing.T) {
	t.Parallel()

	require.Nil(t, CallUsage{InputTokens: new(0)}.Apply(nil))

	existing := map[string]any{"claude": map[string]any{}}
	require.Equal(t, existing, CallUsage{}.Apply(existing))

	update := acp.SessionUpdate{UsageUpdate: &acp.SessionUsageUpdate{
		Size: 200000,
		Used: 1250,
		Meta: CallUsage{InputTokens: new(12), CachedReadTokens: new(1000), CachedWriteTokens: new(0), OutputTokens: new(238)}.Apply(nil),
	}}

	encoded, err := json.Marshal(update)
	require.NoError(t, err)
	require.JSONEq(t, `{"sessionUpdate":"usage_update","size":200000,"used":1250,"_meta":{"acp-go.dev/callUsage":{"inputTokens":12,"cachedReadTokens":1000,"cachedWriteTokens":0,"outputTokens":238}}}`, string(encoded))

	partial, err := json.Marshal(CallUsage{CachedReadTokens: new(5), OutputTokens: new(7)}.Apply(nil))
	require.NoError(t, err)
	require.JSONEq(t, `{"acp-go.dev/callUsage":{"cachedReadTokens":5,"outputTokens":7}}`, string(partial))

	merged := CallUsage{OutputTokens: new(1)}.Apply(map[string]any{"claude": map[string]any{"structuredOutput": true}})
	require.Len(t, merged, 2)
}
