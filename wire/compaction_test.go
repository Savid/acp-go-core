package wire

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

type compactionRecorder struct {
	updates []acp.SessionNotification
	err     error
}

func (r *compactionRecorder) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.updates = append(r.updates, notification)

	return r.err
}

func TestCompactionCarrierRoundTrip(t *testing.T) {
	t.Parallel()
	for _, value := range []Compaction{
		{CompactionID: "attempt", Status: CompactionCompleted},
		{CompactionID: "attempt", Status: CompactionCompleted, Trigger: "auto", ContextBefore: new(120), ContextAfter: new(0)},
	} {
		encoded, err := json.Marshal(CompactionCarrier("session", value))
		require.NoError(t, err)
		var decoded acp.SessionNotification
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		roundTrip, err := json.Marshal(decoded)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), string(roundTrip))
		var frame map[string]any
		require.NoError(t, json.Unmarshal(encoded, &frame))
		require.Equal(t, map[string]any{"sessionUpdate": "session_info_update"}, frame["update"])
		require.Len(t, decoded.Meta, 1)
		actual, err := json.Marshal(decoded.Meta[CompactionKey])
		require.NoError(t, err)
		expected, err := json.Marshal(value)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(actual))
		if value.ContextBefore == nil {
			require.NotContains(t, string(actual), "contextBefore")
		}
	}
}

func TestCompactionsCorrelateAndDeduplicate(t *testing.T) {
	t.Parallel()
	var attempts Compactions
	rec := &compactionRecorder{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := Compaction{Status: CompactionInProgress, Trigger: "manual", ContextBefore: new(120)}
	for _, status := range []string{CompactionInProgress, CompactionInProgress, CompactionCancelled, CompactionCancelled, CompactionInProgress} {
		update := Compaction{Status: status}
		if status == CompactionInProgress {
			update = start
		}
		require.NoError(t, attempts.Publish(ctx, rec, "session", "first", update))
	}
	require.NoError(t, attempts.Publish(ctx, rec, "session", "retry", Compaction{Status: CompactionFailed}))
	require.NoError(t, attempts.Publish(ctx, rec, "session", "next", Compaction{Status: CompactionCompleted, ContextAfter: new(0)}))
	require.Len(t, rec.updates, 4)
	first, ok := rec.updates[0].Meta[CompactionKey].(Compaction)
	require.True(t, ok)
	terminal, ok := rec.updates[1].Meta[CompactionKey].(Compaction)
	require.True(t, ok)
	require.NotEmpty(t, first.CompactionID)
	require.Equal(t, first.CompactionID, terminal.CompactionID)
	require.Equal(t, "manual", terminal.Trigger)
	require.Equal(t, 120, *terminal.ContextBefore)
	require.Equal(t, CompactionCancelled, terminal.Status)
	retry, ok := rec.updates[2].Meta[CompactionKey].(Compaction)
	require.True(t, ok)
	require.NotEqual(t, first.CompactionID, retry.CompactionID)
	var restarted Compactions
	require.NoError(t, restarted.Publish(ctx, rec, "session", "first", start))
	fresh, ok := rec.updates[4].Meta[CompactionKey].(Compaction)
	require.True(t, ok)
	require.NotEqual(t, first.CompactionID, fresh.CompactionID)
}

func TestCompactionsValidateAndDoNotRetryDelivery(t *testing.T) {
	t.Parallel()
	var attempts Compactions
	rec := &compactionRecorder{err: errors.New("closed")}
	for _, value := range []Compaction{{Status: ""}, {Status: CompactionCompleted, Trigger: "unknown"}, {Status: CompactionCompleted, ContextBefore: new(-1)}} {
		require.Error(t, attempts.Publish(t.Context(), rec, "session", "key", value))
	}
	require.Error(t, attempts.Publish(t.Context(), rec, "session", "", Compaction{Status: CompactionInProgress}))
	require.Empty(t, rec.updates)
	require.Equal(t, rec.err, attempts.Publish(t.Context(), rec, "session", "key", Compaction{Status: CompactionCompleted}))
	require.NoError(t, attempts.Publish(t.Context(), rec, "session", "key", Compaction{Status: CompactionCompleted}))
	require.Len(t, rec.updates, 1)
}

func TestCompactionsUncorrelatedOutcomes(t *testing.T) {
	t.Parallel()
	var attempts Compactions
	rec := &compactionRecorder{}
	ids := make(map[string]bool)
	for _, status := range []string{CompactionCompleted, CompactionFailed, CompactionCancelled, CompactionCompleted} {
		require.NoError(t, attempts.Publish(t.Context(), rec, "session", "", Compaction{Status: status}))
		value, ok := rec.updates[len(rec.updates)-1].Meta[CompactionKey].(Compaction)
		require.True(t, ok)
		require.Equal(t, status, value.Status)
		require.NotEmpty(t, value.CompactionID)
		require.False(t, ids[value.CompactionID])
		ids[value.CompactionID] = true
	}
	require.Len(t, rec.updates, 4)
}
