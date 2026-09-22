package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// checkpoint is the encoded form of a reducer: the projection it proved and the
// ordering facts it needs to keep proving it. The frames it reduced are not
// part of it. They serve only to tell an exact retransmission from a
// conflicting reuse of an identity, and carrying them would make a checkpoint
// as large as the history it stands in for.
type checkpoint struct {
	State        checkpointState      `json:"state"`
	Base         uint64               `json:"base"`
	Started      bool                 `json:"started"`
	Failed       *checkpointViolation `json:"failed,omitempty"`
	TurnSeen     []string             `json:"turnSeen"`
	ActivitySeen []string             `json:"activitySeen"`
	BlockedCycle string               `json:"blockedCycle"`
	ActionCycle  map[string]string    `json:"actionCycle"`
	Retired      []string             `json:"retired"`
}

// checkpointState carries every member of State, including the correlation
// members the wire projection leaves unencoded.
type checkpointState struct {
	StreamID                  string                `json:"streamId"`
	ReducedThrough            uint64                `json:"reducedThrough"`
	SuppressedRetransmissions int                   `json:"suppressedRetransmissions"`
	Foreground                *checkpointForeground `json:"foreground"`
	Turns                     []checkpointTurn      `json:"turns"`
	Activities                []checkpointActivity  `json:"activities"`
	Actions                   []ActionRecord        `json:"actions"`
	Closed                    bool                  `json:"closed"`
}

type checkpointForeground struct {
	State   ForegroundState `json:"state"`
	CycleID string          `json:"cycleId"`
	TurnID  string          `json:"turnId"`
	Origin  Cause           `json:"origin"`
}

type checkpointTurn struct {
	TurnID       string  `json:"turnId"`
	Origin       Cause   `json:"origin"`
	Terminal     bool    `json:"terminal"`
	Outcome      Outcome `json:"outcome"`
	SubmissionID string  `json:"submissionId"`
	ClientNonce  string  `json:"clientNonce"`
	RunID        string  `json:"runId"`
	CycleID      string  `json:"cycleId"`
	StopReason   string  `json:"stopReason"`
}

type checkpointActivity struct {
	ActivityID   string        `json:"activityId"`
	Kind         ActivityKind  `json:"kind"`
	State        ActivityState `json:"state"`
	ParentID     string        `json:"parentId"`
	ToolCallID   string        `json:"toolCallId"`
	Cause        Cause         `json:"cause"`
	OriginTurnID string        `json:"originTurnId"`
	RunID        string        `json:"runId"`
	// Progress keeps the exact bytes the source rendered; encoding it as a raw
	// message would compact them.
	Progress []byte `json:"progress,omitempty"`
}

type checkpointViolation struct {
	Kind     ViolationKind `json:"kind"`
	StreamID string        `json:"streamId"`
	Sequence uint64        `json:"sequence"`
	Detail   string        `json:"detail"`
}

// Checkpoint encodes the reducer so a host can persist a long-lived session's
// reduction and continue it from the next sequence instead of replaying the
// incarnation from its opening snapshot. A latched reducer checkpoints its
// refusal and restores latched.
//
// A restored reducer compares a repeated identity with its earlier frame only
// for what it reduced after restoring. An identity at or below the checkpoint
// is suppressed as a retransmission without comparison, so a host that restores
// owes the conflicting-duplicate check for that prefix itself.
func (r *Reducer) Checkpoint() ([]byte, error) {
	encoded := checkpoint{
		State:        encodeCheckpointState(r.state),
		Base:         r.base,
		Started:      r.started,
		TurnSeen:     slices.Sorted(maps.Keys(r.turnSeen)),
		ActivitySeen: slices.Sorted(maps.Keys(r.activitySeen)),
		BlockedCycle: r.blockedCycle,
		ActionCycle:  maps.Clone(r.actionCycle),
		Retired:      slices.Sorted(maps.Keys(r.retired)),
	}
	if encoded.ActionCycle == nil {
		encoded.ActionCycle = map[string]string{}
	}

	if r.failed != nil {
		encoded.Failed = &checkpointViolation{
			Kind: r.failed.Kind, StreamID: r.failed.StreamID,
			Sequence: r.failed.Sequence, Detail: r.failed.Detail,
		}
	}

	data, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("encoding lifecycle checkpoint: %w", err)
	}

	return data, nil
}

// RestoreReducer rebuilds a reducer from a checkpoint under the same negotiated
// configuration it was reducing against.
func RestoreReducer(opts Options, data []byte) (*Reducer, error) {
	var encoded checkpoint
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil, fmt.Errorf("decoding lifecycle checkpoint: %w", err)
	}

	if encoded.Started && encoded.State.StreamID == "" {
		return nil, errors.New("lifecycle checkpoint names no stream for a started reduction")
	}

	if encoded.Started && encoded.State.ReducedThrough < encoded.Base {
		return nil, errors.New("lifecycle checkpoint reduced less than its snapshot boundary")
	}

	reducer := NewReducer(opts)
	reducer.state = decodeCheckpointState(encoded.State)
	reducer.base = encoded.Base
	reducer.started = encoded.Started
	reducer.blockedCycle = encoded.BlockedCycle
	reducer.restored = encoded.State.ReducedThrough

	for _, turnID := range encoded.TurnSeen {
		reducer.turnSeen[turnID] = struct{}{}
	}

	for _, activityID := range encoded.ActivitySeen {
		reducer.activitySeen[activityID] = struct{}{}
	}

	maps.Copy(reducer.actionCycle, encoded.ActionCycle)

	for _, streamID := range encoded.Retired {
		reducer.retired[streamID] = struct{}{}
	}

	if encoded.Failed != nil {
		reducer.failed = violation(
			encoded.Failed.Kind, encoded.Failed.StreamID, encoded.Failed.Sequence, encoded.Failed.Detail,
		)
	}

	return reducer, nil
}

func encodeCheckpointState(state State) checkpointState {
	encoded := checkpointState{
		StreamID:                  state.StreamID,
		ReducedThrough:            state.ReducedThrough,
		SuppressedRetransmissions: state.SuppressedRetransmissions,
		Turns:                     make([]checkpointTurn, 0, len(state.Turns)),
		Activities:                make([]checkpointActivity, 0, len(state.Activities)),
		Actions:                   cloneRecords(state.Actions),
		Closed:                    state.Closed,
	}
	if state.Foreground != nil {
		encoded.Foreground = &checkpointForeground{
			State: state.Foreground.State, CycleID: state.Foreground.CycleID,
			TurnID: state.Foreground.TurnID, Origin: state.Foreground.Origin,
		}
	}

	for index := range state.Turns {
		encoded.Turns = append(encoded.Turns, checkpointTurn(state.Turns[index]))
	}

	for index := range state.Activities {
		activity := &state.Activities[index]
		encoded.Activities = append(encoded.Activities, checkpointActivity{
			ActivityID: activity.ActivityID, Kind: activity.Kind, State: activity.State,
			ParentID: activity.ParentID, ToolCallID: activity.ToolCallID, Cause: activity.Cause,
			OriginTurnID: activity.OriginTurnID, RunID: activity.RunID,
			Progress: []byte(slices.Clone(activity.Progress)),
		})
	}

	return encoded
}

func decodeCheckpointState(encoded checkpointState) State {
	state := State{
		StreamID:                  encoded.StreamID,
		ReducedThrough:            encoded.ReducedThrough,
		SuppressedRetransmissions: encoded.SuppressedRetransmissions,
		Turns:                     make([]TurnRecord, 0, len(encoded.Turns)),
		Activities:                make([]ActivityRecord, 0, len(encoded.Activities)),
		Actions:                   cloneRecords(encoded.Actions),
		Closed:                    encoded.Closed,
	}
	if encoded.Foreground != nil {
		state.Foreground = &Foreground{
			State: encoded.Foreground.State, CycleID: encoded.Foreground.CycleID,
			TurnID: encoded.Foreground.TurnID, Origin: encoded.Foreground.Origin,
		}
	}

	for index := range encoded.Turns {
		state.Turns = append(state.Turns, TurnRecord(encoded.Turns[index]))
	}

	for index := range encoded.Activities {
		activity := &encoded.Activities[index]
		state.Activities = append(state.Activities, ActivityRecord{
			ActivityID: activity.ActivityID, Kind: activity.Kind, State: activity.State,
			ParentID: activity.ParentID, ToolCallID: activity.ToolCallID, Cause: activity.Cause,
			OriginTurnID: activity.OriginTurnID, RunID: activity.RunID,
			Progress: json.RawMessage(slices.Clone(activity.Progress)),
		})
	}

	return state
}
