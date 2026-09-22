package lifecycle

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A reducer checkpointed and restored between every pair of inputs proves what
// an unbroken reducer proves: the same refusal at the same input and the same
// projection after every step. The comparison stops where a vector repeats an
// identity, because the frames a repeat is judged against are not part of a
// checkpoint; that boundary is its own contract, covered below.
func TestReducerCheckpointContinuesEveryFixtureVector(t *testing.T) {
	t.Parallel()

	for _, entry := range loadManifest(t).Fixtures {
		t.Run(entry.File, func(t *testing.T) {
			t.Parallel()

			vector := loadFixture(t, entry.File)
			opts := Options{Negotiated: vector.Negotiated.Negotiated}
			unbroken := NewReducer(opts)
			restored := NewReducer(opts)

			for index, input := range vector.Input {
				restored = restoreCheckpoint(t, opts, unbroken, restored)

				if input.Control != "" {
					require.Equal(t, controlSessionClosed, input.Control, "unknown control event")
					unbroken.Close()
					restored.Close()

					continue
				}

				require.Equal(t, notificationMethod, input.Method)

				suppressed := unbroken.State().SuppressedRetransmissions
				expected := unbroken.ReduceSessionUpdate(input.Params)
				if unbroken.State().SuppressedRetransmissions != suppressed || refusalKind(expected) == ViolationConflictingDuplicate {
					return
				}

				actual := restored.ReduceSessionUpdate(input.Params)
				require.Equal(t, expected, actual, "input %d", index)
				require.Equal(t, unbroken.State(), restored.State(), "input %d", index)

				if expected != nil {
					restored = restoreCheckpoint(t, opts, unbroken, restored)
					requireLatched(t, restored, vector, refusalOf(t, expected))

					return
				}
			}

			requireStateEquals(t, vector.Expect.State, restored.State())
		})
	}
}

// A repeated identity below the checkpoint cannot be compared with the frame it
// once delivered, so the restored reducer takes it as a retransmission; what it
// reduced itself is compared as before.
func TestRestoredReducerSuppressesAnIdentityBelowItsCheckpoint(t *testing.T) {
	t.Parallel()

	vector, inputs := acceptedFixtureWithDeltas(t)
	opts := Options{Negotiated: vector.Negotiated.Negotiated}
	unbroken := NewReducer(opts)
	require.NoError(t, unbroken.ReduceSessionUpdate(inputs[0].Params))
	require.NoError(t, unbroken.ReduceSessionUpdate(inputs[1].Params))

	restored := restoreCheckpoint(t, opts, unbroken, unbroken)
	require.NoError(t, restored.ReduceSessionUpdate(inputs[1].Params))
	require.Equal(t, 1, restored.State().SuppressedRetransmissions)

	require.NoError(t, restored.ReduceSessionUpdate(inputs[2].Params))
	require.NoError(t, restored.ReduceSessionUpdate(inputs[2].Params),
		"a frame reduced after restoring is compared and suppressed")
	require.Equal(t, 2, restored.State().SuppressedRetransmissions)

	conflicting, err := conflictingRetransmission(inputs[2].Params)
	require.NoError(t, err)

	var refusal *ViolationError

	require.True(t, errors.As(restored.ReduceSessionUpdate(conflicting), &refusal))
	require.Equal(t, ViolationConflictingDuplicate, refusal.Kind)
}

func TestRestoreReducerRefusesAMalformedCheckpoint(t *testing.T) {
	t.Parallel()

	_, err := RestoreReducer(Options{}, []byte(`{"state":`))
	require.Error(t, err)

	_, err = RestoreReducer(Options{}, []byte(`{"state":{"streamId":""},"started":true}`))
	require.Error(t, err, "a started reduction names its stream")

	_, err = RestoreReducer(Options{}, []byte(`{"state":{"streamId":"s","reducedThrough":1},"base":3,"started":true}`))
	require.Error(t, err, "a reduction cannot stand below its snapshot boundary")
}

// restoreCheckpoint rebuilds reducer from its checkpoint and proves the rebuilt
// projection matches reference, the reducer the checkpointed one has been
// shadowing.
func restoreCheckpoint(t *testing.T, opts Options, reference, reducer *Reducer) *Reducer {
	t.Helper()

	data, err := reducer.Checkpoint()
	require.NoError(t, err)

	restored, err := RestoreReducer(opts, data)
	require.NoError(t, err)
	require.Equal(t, reference.State(), restored.State())

	return restored
}

func refusalKind(err error) ViolationKind {
	var refusal *ViolationError
	if errors.As(err, &refusal) {
		return refusal.Kind
	}

	return ""
}

func refusalOf(t *testing.T, err error) *ViolationError {
	t.Helper()

	var refusal *ViolationError

	require.True(t, errors.As(err, &refusal), "%v", err)

	return refusal
}

// acceptedFixtureWithDeltas picks a vector that opens a stream and reduces at
// least two deltas after it without closing the session.
func acceptedFixtureWithDeltas(t *testing.T) (fixture, []fixtureInput) {
	t.Helper()

	for _, entry := range loadManifest(t).Fixtures {
		vector := loadFixture(t, entry.File)
		if vector.Expect.Verdict != "accepted" || len(vector.Input) < 3 {
			continue
		}

		controlled := false
		for _, input := range vector.Input[:3] {
			controlled = controlled || input.Control != ""
		}

		if !controlled {
			return vector, vector.Input[:3]
		}
	}

	t.Fatal("the battery has no accepted vector with two deltas")

	return fixture{}, nil
}

// conflictingRetransmission reuses a notification's identity under a frame that
// differs from the one delivered.
func conflictingRetransmission(params json.RawMessage) (json.RawMessage, error) {
	var frame map[string]any
	if err := json.Unmarshal(params, &frame); err != nil {
		return nil, err
	}

	frame["conflict"] = true

	return json.Marshal(frame)
}
