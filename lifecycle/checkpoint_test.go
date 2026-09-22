package lifecycle

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// deltaVector opens a stream and reduces at least two deltas after it without
// closing the session, so a checkpoint can be taken between accepted frames.
const deltaVector = "agent-origin-second-turn.json"

// A reducer checkpointed and restored between every pair of inputs proves what
// an unbroken reducer proves: the same verdict at every input, the same
// projection after every step, and the same ordering facts behind it.
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

				expected := unbroken.ReduceSessionUpdate(input.Params)
				actual := restored.ReduceSessionUpdate(input.Params)
				require.Equal(t, expected, actual, "input %d", index)
				requireReducersEqual(t, unbroken, restored)

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

// A repeated identity below the checkpoint is judged against the digest of the
// frame it delivered: an exact retransmission is suppressed and a conflicting
// one is refused, as an unbroken reducer would.
func TestRestoredReducerJudgesARepeatBelowItsCheckpoint(t *testing.T) {
	t.Parallel()

	vector, inputs := deltaVectorInputs(t)
	opts := Options{Negotiated: vector.Negotiated.Negotiated}
	unbroken := NewReducer(opts)
	require.NoError(t, unbroken.ReduceSessionUpdate(inputs[0].Params))
	require.NoError(t, unbroken.ReduceSessionUpdate(inputs[1].Params))

	restored := restoreCheckpoint(t, opts, unbroken, unbroken)
	require.NoError(t, restored.ReduceSessionUpdate(inputs[1].Params))
	require.Equal(t, 1, restored.State().SuppressedRetransmissions)

	conflicting, err := conflictingRetransmission(inputs[1].Params)
	require.NoError(t, err)

	refusal := refusalOf(t, restored.ReduceSessionUpdate(conflicting))
	require.Equal(t, ViolationConflictingDuplicate, refusal.Kind)
	require.Equal(t, refusal, refusalOf(t, unbroken.ReduceSessionUpdate(conflicting)))
}

func TestRestoreReducerRefusesAMalformedCheckpoint(t *testing.T) {
	t.Parallel()

	vector, inputs := deltaVectorInputs(t)
	opts := Options{Negotiated: vector.Negotiated.Negotiated}
	reducer := NewReducer(opts)
	require.NoError(t, reducer.ReduceSessionUpdate(inputs[0].Params))
	require.NoError(t, reducer.ReduceSessionUpdate(inputs[1].Params))

	started, err := reducer.Checkpoint()
	require.NoError(t, err)

	unstarted, err := NewReducer(opts).Checkpoint()
	require.NoError(t, err)

	for _, data := range [][]byte{[]byte(`{"state":`), []byte(`null`), []byte(`[]`), []byte(``)} {
		_, err = RestoreReducer(opts, data)
		require.Error(t, err, "%s", data)
	}

	_, err = RestoreReducer(opts, append(append([]byte{}, started...), ' ', '{', '}'))
	require.Error(t, err, "trailing content")

	other := opts
	other.Negotiated.ActivityKinds = append([]ActivityKind{ActivityTask}, other.Negotiated.ActivityKinds...)
	_, err = RestoreReducer(other, started)
	require.Error(t, err, "another negotiated configuration")

	mutations := map[string]func(document map[string]any){
		"unknown member": func(document map[string]any) { document["extra"] = true },
		"started reduction names no stream": func(document map[string]any) {
			member(document, "state")["streamId"] = ""
		},
		"reduced less than its snapshot boundary": func(document map[string]any) {
			document["base"] = float64(1 << 40)
		},
		"violation outside the vocabulary": func(document map[string]any) {
			document["failed"] = map[string]any{"kind": "not_a_token", "streamId": "s", "sequence": 1, "detail": ""}
		},
		"frame digest of the wrong size": func(document map[string]any) {
			frames(document)[0]["digest"] = "AAAA"
		},
		"frame outside the reduced range": func(document map[string]any) {
			frames(document)[0]["sequence"] = float64(1 << 40)
		},
		"one sequence twice": func(document map[string]any) {
			frames(document)[1]["sequence"] = frames(document)[0]["sequence"]
		},
		"missing reduced frame": func(document map[string]any) {
			document["frames"] = frames(document)[1:]
		},
	}
	for name, mutate := range mutations {
		_, err = RestoreReducer(opts, mutated(t, started, mutate))
		require.Error(t, err, name)
	}

	unstartedMutations := map[string]func(document map[string]any){
		"unstarted reduction reduced a sequence": func(document map[string]any) {
			member(document, "state")["reducedThrough"] = float64(1)
		},
		"unstarted reduction carries a turn": func(document map[string]any) {
			member(document, "state")["turns"] = []any{map[string]any{fieldTurnID: "ghost"}}
		},
		"unstarted reduction carries a frame": func(document map[string]any) {
			document["frames"] = []any{map[string]any{"sequence": float64(1), "digest": make([]byte, sha256.Size)}}
		},
	}
	for name, mutate := range unstartedMutations {
		_, err = RestoreReducer(opts, mutated(t, unstarted, mutate))
		require.Error(t, err, name)
	}
}

// Every member of the projection, including the correlation members the wire
// projection leaves unencoded, survives a checkpoint.
func TestCheckpointCarriesEveryProjectionMember(t *testing.T) {
	t.Parallel()

	state := State{
		StreamID:                  "strm-1",
		ReducedThrough:            7,
		SuppressedRetransmissions: 2,
		Foreground:                &Foreground{State: ForegroundRunning, CycleID: "cyc-1", TurnID: "turn-1", Origin: CauseSubmission},
		Turns: []TurnRecord{{
			TurnID: "turn-1", Origin: CauseSubmission, Terminal: true, Outcome: OutcomeSuccess,
			SubmissionID: "sub-1", ClientNonce: "nonce-1", RunID: "run-1", CycleID: "cyc-1", StopReason: "end_turn",
		}},
		Activities: []ActivityRecord{{
			ActivityID: "act-1", Kind: ActivityTask, State: ActivityRunning, ParentID: "act-0",
			ToolCallID: "call-1", Cause: CauseSubmission, OriginTurnID: "turn-1", RunID: "run-1",
			Progress: json.RawMessage("{\n  \"stage\": \"<final>\"\n}"),
		}},
		Actions: []ActionRecord{{
			ActionID: "action-1", Kind: ActionPermission, State: ActionPending,
			Owner: Owner{Type: OwnerTurn, ID: "agent-1"}, RunID: "run-1", BlocksForeground: true,
		}},
		Closed: true,
	}

	data, err := json.Marshal(encodeCheckpointState(state))
	require.NoError(t, err)

	var encoded checkpointState

	require.NoError(t, json.Unmarshal(data, &encoded))
	require.Equal(t, state, decodeCheckpointState(encoded))
}

// A digest is equal exactly when lifecycle value equality holds.
func TestFrameDigestMatchesValueEquality(t *testing.T) {
	t.Parallel()

	cases := []struct {
		left, right string
		equal       bool
	}{
		{`{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1}`, true},
		{`{"a": 1}`, `{"a":1}`, true},
		{`1.0`, `1`, true},
		{`1e2`, `100`, true},
		{`-0`, `0.0`, true},
		{`1e99999999999999999999`, `1e99999999999999999999`, true},
		{`1e99999999999999999999`, `1E99999999999999999999`, false},
		{`"1"`, `1`, false},
		{`[1,2]`, `[2,1]`, false},
		{`{"a":null}`, `{}`, false},
		{`null`, `false`, false},
		{`{"a":{"b":"x"}}`, `{"a":{"b":"x "}}`, false},
		{`["a","b"]`, `["ab",""]`, false},
	}
	for _, tc := range cases {
		left, ok := decodeValue(json.RawMessage(tc.left))
		require.True(t, ok, tc.left)

		right, ok := decodeValue(json.RawMessage(tc.right))
		require.True(t, ok, tc.right)

		require.Equal(t, tc.equal, valueEqual(left, right), "%s vs %s", tc.left, tc.right)
		require.Equal(t, tc.equal, frameDigest(left) == frameDigest(right), "%s vs %s", tc.left, tc.right)
	}
}

// restoreCheckpoint rebuilds reducer from its checkpoint and proves the rebuilt
// reducer matches reference, the reducer the checkpointed one has been
// shadowing.
func restoreCheckpoint(t *testing.T, opts Options, reference, reducer *Reducer) *Reducer {
	t.Helper()

	data, err := reducer.Checkpoint()
	require.NoError(t, err)

	restored, err := RestoreReducer(opts, data)
	require.NoError(t, err)
	requireReducersEqual(t, reference, restored)

	return restored
}

// reducerFacts is every fact a reducer proves with, beside the projection it
// reports.
type reducerFacts struct {
	negotiated   Negotiated
	base         uint64
	started      bool
	failed       *ViolationError
	frames       map[uint64][sha256.Size]byte
	turnSeen     map[string]struct{}
	activitySeen map[string]struct{}
	blockedCycle string
	actionCycle  map[string]string
	retired      map[string]struct{}
}

func requireReducersEqual(t *testing.T, expected, actual *Reducer) {
	t.Helper()

	require.Equal(t, expected.State(), actual.State())
	require.Equal(t, facts(expected), facts(actual))
}

func facts(r *Reducer) reducerFacts {
	return reducerFacts{
		negotiated: r.negotiated, base: r.base, started: r.started, failed: r.failed, frames: r.frames,
		turnSeen: r.turnSeen, activitySeen: r.activitySeen, blockedCycle: r.blockedCycle,
		actionCycle: r.actionCycle, retired: r.retired,
	}
}

func refusalOf(t *testing.T, err error) *ViolationError {
	t.Helper()

	var refusal *ViolationError

	require.True(t, errors.As(err, &refusal), "%v", err)

	return refusal
}

func deltaVectorInputs(t *testing.T) (fixture, []fixtureInput) {
	t.Helper()

	vector := loadFixture(t, deltaVector)
	require.Equal(t, "accepted", vector.Expect.Verdict)
	require.GreaterOrEqual(t, len(vector.Input), 3)

	for _, input := range vector.Input[:3] {
		require.Empty(t, input.Control)
	}

	return vector, vector.Input[:3]
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

// mutated applies one edit to a decoded checkpoint document and re-encodes it.
func mutated(t *testing.T, data []byte, mutate func(document map[string]any)) []byte {
	t.Helper()

	var document map[string]any

	require.NoError(t, json.Unmarshal(data, &document))
	mutate(document)

	encoded, err := json.Marshal(document)
	require.NoError(t, err)

	return encoded
}

func member(document map[string]any, key string) map[string]any {
	object, _ := document[key].(map[string]any)

	return object
}

func frames(document map[string]any) []map[string]any {
	list, _ := document["frames"].([]any)
	out := make([]map[string]any, 0, len(list))

	for _, entry := range list {
		object, _ := entry.(map[string]any)
		out = append(out, object)
	}

	return out
}
