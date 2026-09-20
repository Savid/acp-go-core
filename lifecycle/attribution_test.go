package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func attributionStream(t *testing.T) (Negotiated, func() []json.RawMessage, func(map[string]any)) {
	t.Helper()

	negotiated := Negotiated{Version: Version, UpdatesOutsidePrompt: true, ActivityKinds: []ActivityKind{}}

	var frames []json.RawMessage

	record := func(params map[string]any) {
		encoded, err := json.Marshal(params)
		require.NoError(t, err)
		frames = append(frames, encoded)
	}

	var publisher Publisher

	deliver := func(_ context.Context, envelope map[string]any) error {
		record(map[string]any{
			"sessionId": "s",
			"update":    map[string]any{"sessionUpdate": "session_info_update"},
			"_meta":     map[string]any{MetaKey: envelope},
		})

		return nil
	}
	require.NoError(t, publisher.Open(t.Context(), "s:1", negotiated, deliver))

	cycle := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), cycle))

	return negotiated, func() []json.RawMessage { return frames }, func(update map[string]any) {
		record(map[string]any{"sessionId": "s", "update": update})
		if update["sessionUpdate"] == updateUsage {
			require.NoError(t, publisher.Idle(t.Context(), cycle, "end_turn", OutcomeSuccess))
		}
	}
}

func TestCheckAttributionAcceptsContentInsideTheForeground(t *testing.T) {
	t.Parallel()

	negotiated, frames, emit := attributionStream(t)
	emit(map[string]any{"sessionUpdate": updateAgentMessageChunk, "content": map[string]any{"type": "text", "text": "hi"}})
	emit(map[string]any{"sessionUpdate": updateUsage, "used": 1, "size": 2})

	require.NoError(t, CheckAttribution(negotiated, frames()))
}

func TestCheckAttributionRefusesContentOutsideTheForeground(t *testing.T) {
	t.Parallel()

	negotiated, frames, emit := attributionStream(t)
	emit(map[string]any{"sessionUpdate": updateUsage, "used": 1, "size": 2})
	stray, err := json.Marshal(map[string]any{"sessionId": "s", "update": map[string]any{"sessionUpdate": updateAgentMessageChunk, "content": map[string]any{"type": "text", "text": "late"}}})
	require.NoError(t, err)

	err = CheckAttribution(negotiated, append(frames(), stray))
	require.ErrorContains(t, err, "agent_message_chunk arrived with no live foreground")
}

func TestCheckAttributionIgnoresSessionScopedUpdates(t *testing.T) {
	t.Parallel()

	negotiated := Negotiated{Version: Version, ActivityKinds: []ActivityKind{}}
	commands, err := json.Marshal(map[string]any{"sessionId": "s", "update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{}}})
	require.NoError(t, err)

	require.NoError(t, CheckAttribution(negotiated, []json.RawMessage{commands}))
}

func TestCheckAttributionRefusesVendorHints(t *testing.T) {
	t.Parallel()

	negotiated, frames, emit := attributionStream(t)
	emit(map[string]any{"sessionUpdate": updateUsage, "used": 1, "size": 2})
	hinted, err := json.Marshal(map[string]any{
		"sessionId": "s",
		"update":    map[string]any{"sessionUpdate": updateAgentMessageChunk, "content": map[string]any{"type": "text", "text": "hi"}},
		"_meta":     map[string]any{"codex": map[string]any{"turnId": "t"}},
	})
	require.NoError(t, err)

	err = CheckAttribution(negotiated, append(frames(), hinted))
	require.ErrorContains(t, err, "_meta.codex.turnId correlates content by hint")
}
