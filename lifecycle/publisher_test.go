package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublisherSerializesForegroundAndBlockers(t *testing.T) {
	t.Parallel()
	var publisher Publisher
	var envelopes []map[string]any
	// The emitter validates every published event through the canonical reducer.
	err := publisher.Open(t.Context(), "incarnation", Negotiated{Version: Version, UpdatesOutsidePrompt: true, ActivityKinds: []ActivityKind{}}, func(_ context.Context, envelope map[string]any) error {
		envelopes = append(envelopes, envelope)

		return nil
	})
	require.NoError(t, err)
	cycle := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), cycle))
	require.NoError(t, publisher.ActionPending(t.Context(), cycle, "one", ActionPermission))
	require.NoError(t, publisher.ActionPending(t.Context(), cycle, "two", ActionElicitation))
	require.NoError(t, publisher.ActionResolved(t.Context(), cycle, "one", ActionAccepted))
	require.NoError(t, publisher.Idle(t.Context(), cycle, StopReasonEndTurn, OutcomeSuccess))
	require.NotEmpty(t, envelopes)
	require.Empty(t, publisher.blockers)
	publisher.Fence()
	require.False(t, publisher.Active())
}

func TestPublisherKeepsBlockersPerCycle(t *testing.T) {
	t.Parallel()
	var publisher Publisher
	var transitions []string
	deliver := func(_ context.Context, envelope map[string]any) error {
		event, _ := envelope["event"].(map[string]any)
		if event["type"] == "state_update" {
			state, _ := event["state"].(string)
			transitions = append(transitions, state)
		}

		return nil
	}
	require.NoError(t, publisher.Open(t.Context(), "incarnation", Negotiated{Version: Version, UpdatesOutsidePrompt: true, ActivityKinds: []ActivityKind{}}, deliver))
	first := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), first))
	require.NoError(t, publisher.ActionPending(t.Context(), first, "stale", ActionPermission))
	require.NoError(t, publisher.Idle(t.Context(), first, StopReasonEndTurn, OutcomeSuccess))
	require.Empty(t, publisher.blockers, "idle cancels the cycle's own blockers")
	second := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), second))
	require.NoError(t, publisher.ActionPending(t.Context(), second, "fresh", ActionElicitation))
	require.NoError(t, publisher.ActionResolved(t.Context(), first, "fresh", ActionAccepted), "another cycle cannot resolve this cycle's blocker")
	require.Len(t, publisher.blockers, 1)
	require.NoError(t, publisher.ActionResolved(t.Context(), second, "fresh", ActionAccepted))
	require.NoError(t, publisher.Idle(t.Context(), second, StopReasonEndTurn, OutcomeSuccess))
	require.Equal(t, []string{"running", "requires_action", "idle", "running", "requires_action", "running", "idle"}, transitions)
	require.True(t, publisher.Active())
}

func TestPublisherCountsBlockersPerCycle(t *testing.T) {
	t.Parallel()
	var publisher Publisher
	var transitions []string
	deliver := func(_ context.Context, envelope map[string]any) error {
		event, _ := envelope["event"].(map[string]any)
		if event["type"] == "state_update" {
			state, _ := event["state"].(string)
			transitions = append(transitions, state)
		}

		return nil
	}
	require.NoError(t, publisher.Open(t.Context(), "incarnation", Negotiated{Version: Version, UpdatesOutsidePrompt: true, ActivityKinds: []ActivityKind{}}, deliver))
	abandoned := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), abandoned))
	require.NoError(t, publisher.ActionPending(t.Context(), abandoned, "stale", ActionPermission))
	next := publisher.NewAgentCycle()
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), next))
	require.NoError(t, publisher.ActionPending(t.Context(), next, "fresh", ActionElicitation))
	require.Equal(t, "requires_action", transitions[len(transitions)-1], "a blocker left by another cycle does not hide this cycle's first blocker")
	require.NoError(t, publisher.ActionResolved(t.Context(), next, "fresh", ActionAccepted))
	require.Equal(t, "running", transitions[len(transitions)-1], "resolving this cycle's last blocker resumes it despite another cycle's blocker")
	require.Len(t, publisher.blockers, 1)
}

func TestPublisherFencesFailedDelivery(t *testing.T) {
	t.Parallel()
	var publisher Publisher
	failure := errors.New("delivery unavailable")
	err := publisher.Open(t.Context(), "incarnation", Negotiated{Version: Version, UpdatesOutsidePrompt: true, ActivityKinds: []ActivityKind{}}, func(context.Context, map[string]any) error {
		return failure
	})
	require.ErrorIs(t, err, failure)
	require.False(t, publisher.Active())
}

func TestPublisherRejectsAnAgentCycleFromAnotherIncarnation(t *testing.T) {
	t.Parallel()
	var publisher Publisher
	negotiated := Negotiated{Version: Version, UpdatesOutsidePrompt: true}
	require.NoError(t, publisher.Open(t.Context(), "first", negotiated, nil))
	cycle := publisher.NewAgentCycle()
	publisher.Fence()
	require.NoError(t, publisher.Open(t.Context(), "second", negotiated, nil))
	require.Error(t, publisher.OpenAgentCycle(t.Context(), cycle))
	require.True(t, publisher.Active())
	require.NoError(t, publisher.OpenAgentCycle(t.Context(), publisher.NewAgentCycle()))
}
