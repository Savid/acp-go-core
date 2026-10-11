package wire

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestUniformErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  *acp.RequestError
		code int
		data map[string]any
	}{
		{"unsupported", Unsupported("cwd"), -32602, map[string]any{"error": "unsupported", "field": "cwd"}},
		{"missing", Missing("x"), -32602, map[string]any{"error": "missing", "field": "x"}},
		{"unknown session", UnknownSession(), -32602, map[string]any{"error": "unknown session", "field": "sessionId"}},
		{"backpressure", Backpressure("active_sessions"), -32600, map[string]any{"error": "backpressure", "limit": "active_sessions"}},
		{"invalid options", InvalidOptions("pi", "home"), -32603, map[string]any{"error": "pi_invalid_options", "field": "home"}},
		{"invalid options fieldless", InvalidOptions("pi", ""), -32603, map[string]any{"error": "pi_invalid_options"}},
		{"restore failed", RestoreFailed("codex"), -32603, map[string]any{"error": "codex_restore_failed"}},
		{"runtime unavailable", RuntimeUnavailable("codex"), -32603, map[string]any{"error": "codex_runtime_unavailable"}},
		{"poisoned", SessionPoisoned("amp", CauseNativeSessionIdentityDrift), -32603, map[string]any{"error": "amp_session_poisoned", "cause": "native_session_identity_drift"}},
		{"internal", InternalFailure("claude", ClassNativeStart), -32603, map[string]any{"error": "claude_internal_failure", "class": "native_start"}},
		{"authenticate", AuthenticateRefused(acp.AuthenticateRequest{MethodId: "token"}), -32602, map[string]any{"methodId": "token"}},
		{"authenticate lifecycle key", AuthenticateRefused(acp.AuthenticateRequest{MethodId: "token", Meta: map[string]any{LifecycleKey: map[string]any{}}}), -32602,
			map[string]any{"error": "unsupported", "field": `_meta["` + LifecycleKey + `"]`}},
		{"turn failed", TurnFailed("hermes", TurnFailure{Cause: CauseProvider, Message: "boom", StatusCode: 429, ProviderCode: "rate"}), -32603,
			map[string]any{"error": "hermes_turn_failed", "cause": "provider", "message": "boom", "statusCode": 429, "providerCode": "rate"}},
		{"image turn failed", TurnFailed("pi", TurnFailure{Cause: CauseTransport, Message: "gone", Stage: "image_output", Reason: "storage_failed"}), -32603,
			map[string]any{"error": "pi_turn_failed", "cause": "transport", "message": "gone", "stage": "image_output", "reason": "storage_failed"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.code, tc.err.Code)
			require.Equal(t, tc.data, tc.err.Data)
		})
	}
}

func TestRestoreRefusedLogsTheReason(t *testing.T) {
	t.Parallel()

	var logged strings.Builder

	logger := slog.New(slog.NewTextHandler(&logged, nil))
	refusal := RestoreRefused(t.Context(), logger, "pi", "session-1", errors.New("row 3 is not json"))
	require.Equal(t, RestoreFailed("pi"), refusal)
	require.Contains(t, logged.String(), `msg="session restore failed" vendor=pi session_id=session-1 reason="row 3 is not json"`)
}

func TestReservedMeta(t *testing.T) {
	t.Parallel()

	require.NoError(t, CheckReservedMeta(map[string]any{"vendor": map[string]any{}, "traceparent": "x"}))

	err := CheckReservedMeta(map[string]any{HandoffKey: map[string]any{}})
	require.Error(t, err)
	require.Contains(t, err.Error(), HandoffKey)
}

func TestRawEventsSequenceAndCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	events := NewRawEvents("pi", "s", "pi-rpc", true)

	var delivered []map[string]any

	fail := errors.New("transport down")
	notify := func(_ context.Context, method string, params map[string]any) error {
		require.Equal(t, "_pi/rawEvent", method)

		event, ok := params["event"].(map[string]any)
		require.True(t, ok)

		if event["fail"] == true {
			return fail
		}

		delivered = append(delivered, params)

		return nil
	}

	require.NoError(t, events.Emit(ctx, notify, map[string]any{"n": 1}))
	require.ErrorIs(t, events.Emit(ctx, notify, map[string]any{"fail": true}), fail)
	require.NoError(t, events.Emit(ctx, notify, map[string]any{"n": 2}))
	require.NoError(t, events.Emit(ctx, notify, nil))
	require.NoError(t, events.Emit(ctx, notify, map[string]any{"big": strings.Repeat("x", RawEventMaxBytes)}))

	require.Len(t, delivered, 3)
	require.Equal(t, uint64(1), delivered[0]["sequence"])
	require.Equal(t, uint64(2), delivered[1]["sequence"], "a failed delivery does not consume its sequence")
	require.Equal(t, uint64(3), delivered[2]["sequence"])

	marker, ok := delivered[2]["event"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, marker["truncated"])
	require.Equal(t, "oversize", marker["reason"])
	require.Equal(t, RawEventMaxBytes, marker["maxBytes"])
	size, ok := marker["sizeBytes"].(int)
	require.True(t, ok)
	require.Greater(t, size, RawEventMaxBytes)

	unserializable, err := capRawEvent(map[string]any{"sessionId": "s", "sequence": uint64(1), "source": "x", "event": map[string]any{"ch": make(chan int)}})
	require.NoError(t, err)
	unserializableMarker, ok := unserializable["event"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "unserializable", unserializableMarker["reason"])

	disabled := NewRawEvents("pi", "s", "pi-rpc", false)
	require.False(t, disabled.Enabled())
	require.NoError(t, disabled.Emit(ctx, notify, map[string]any{"n": 9}))
	require.Len(t, delivered, 3)
}
