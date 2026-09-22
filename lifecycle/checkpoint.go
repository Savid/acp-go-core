package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
)

// checkpoint is the encoded form of a reducer: the projection it proved, the
// ordering facts it needs to keep proving it, and the configuration it proved
// them against. The frames it reduced are carried as digests under lifecycle
// value equality, so a repeated identity is judged after a restore exactly as
// before one.
type checkpoint struct {
	Negotiated   checkpointNegotiated `json:"negotiated"`
	State        checkpointState      `json:"state"`
	Base         uint64               `json:"base"`
	Started      bool                 `json:"started"`
	Failed       *checkpointViolation `json:"failed,omitempty"`
	Frames       []checkpointFrame    `json:"frames"`
	TurnSeen     []string             `json:"turnSeen"`
	ActivitySeen []string             `json:"activitySeen"`
	BlockedCycle string               `json:"blockedCycle"`
	ActionCycle  map[string]string    `json:"actionCycle"`
	Retired      []string             `json:"retired"`
}

// checkpointNegotiated carries the answer as plain members; the capability
// decoder reads only a wire answer, and a reducer given no answer has none.
type checkpointNegotiated struct {
	Version              int            `json:"version"`
	UpdatesOutsidePrompt bool           `json:"updatesOutsidePrompt"`
	ActivityKinds        []ActivityKind `json:"activityKinds"`
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
	Actions                   []checkpointAction    `json:"actions"`
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

type checkpointAction struct {
	ActionID         string      `json:"actionId"`
	Kind             ActionKind  `json:"kind"`
	State            ActionState `json:"state"`
	Owner            Owner       `json:"owner"`
	RunID            string      `json:"runId"`
	BlocksForeground bool        `json:"blocksForeground"`
}

type checkpointViolation struct {
	Kind     ViolationKind `json:"kind"`
	StreamID string        `json:"streamId"`
	Sequence uint64        `json:"sequence"`
	Detail   string        `json:"detail"`
}

type checkpointFrame struct {
	Sequence uint64 `json:"sequence"`
	Digest   []byte `json:"digest"`
}

// Checkpoint encodes the reducer so a host can persist a long-lived session's
// reduction and continue it from the next sequence instead of replaying the
// incarnation from its opening snapshot. A restored reducer reduces exactly as
// the checkpointed one would have: a latched reducer restores latched, and a
// repeated identity is compared with the frame it delivered. The encoding is
// opaque to the host and restores only under the configuration it was taken
// under.
func (r *Reducer) Checkpoint() ([]byte, error) {
	encoded := checkpoint{
		Negotiated:   checkpointNegotiated(r.negotiated),
		State:        encodeCheckpointState(r.state),
		Base:         r.base,
		Started:      r.started,
		Frames:       make([]checkpointFrame, 0, len(r.frames)),
		TurnSeen:     sortedKeys(r.turnSeen),
		ActivitySeen: sortedKeys(r.activitySeen),
		BlockedCycle: r.blockedCycle,
		ActionCycle:  maps.Clone(r.actionCycle),
		Retired:      sortedKeys(r.retired),
	}

	for _, sequence := range slices.Sorted(maps.Keys(r.frames)) {
		digest := r.frames[sequence]
		encoded.Frames = append(encoded.Frames, checkpointFrame{Sequence: sequence, Digest: digest[:]})
	}

	if r.failed != nil {
		failed := checkpointViolation(*r.failed)
		encoded.Failed = &failed
	}

	data, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("encoding lifecycle checkpoint: %w", err)
	}

	return data, nil
}

// RestoreReducer rebuilds a reducer from a checkpoint. The checkpoint MUST have
// been taken under the same negotiated configuration; a checkpoint taken under
// another, a document carrying a member this module does not know, or one whose
// facts contradict each other is refused.
func RestoreReducer(opts Options, data []byte) (*Reducer, error) {
	encoded, err := decodeCheckpoint(data)
	if err != nil {
		return nil, err
	}

	if !negotiatedEqual(Negotiated(encoded.Negotiated), opts.Negotiated) {
		return nil, errors.New("lifecycle checkpoint was taken under another negotiated configuration")
	}

	frames, err := encoded.validate()
	if err != nil {
		return nil, err
	}

	reducer := NewReducer(opts)
	reducer.state = decodeCheckpointState(encoded.State)
	reducer.base = encoded.Base
	reducer.started = encoded.Started
	reducer.frames = frames
	reducer.blockedCycle = encoded.BlockedCycle
	maps.Copy(reducer.actionCycle, encoded.ActionCycle)

	for _, turnID := range encoded.TurnSeen {
		reducer.turnSeen[turnID] = struct{}{}
	}

	for _, activityID := range encoded.ActivitySeen {
		reducer.activitySeen[activityID] = struct{}{}
	}

	for _, streamID := range encoded.Retired {
		reducer.retired[streamID] = struct{}{}
	}

	if encoded.Failed != nil {
		failed := ViolationError(*encoded.Failed)
		reducer.failed = &failed
	}

	return reducer, nil
}

// decodeCheckpoint reads one checkpoint document strictly: it is a single object
// and every member is one this module wrote.
func decodeCheckpoint(data []byte) (checkpoint, error) {
	var encoded checkpoint

	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return encoded, errors.New("decoding lifecycle checkpoint: not an object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&encoded); err != nil {
		return encoded, fmt.Errorf("decoding lifecycle checkpoint: %w", err)
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return encoded, errors.New("decoding lifecycle checkpoint: trailing content")
	}

	return encoded, nil
}

// validate refuses a checkpoint whose facts no reducer could have reached, and
// returns the frame digests it carries keyed by sequence.
func (c checkpoint) validate() (map[uint64][sha256.Size]byte, error) {
	if !c.Started && c.Failed == nil {
		if err := c.validateUnstarted(); err != nil {
			return nil, err
		}
	} else if c.Started {
		if c.State.StreamID == "" {
			return nil, errors.New("lifecycle checkpoint names no stream for a started reduction")
		}

		if c.State.ReducedThrough < c.Base {
			return nil, errors.New("lifecycle checkpoint reduced less than its snapshot boundary")
		}
	}

	if c.Failed != nil && !slices.Contains(vocabulary, c.Failed.Kind) {
		return nil, errors.New("lifecycle checkpoint names a violation outside the vocabulary")
	}

	return c.validateFrames()
}

// validateUnstarted holds an unstarted, unlatched checkpoint to what such a
// reducer can carry: a stream name and the session's close. A latched one may
// hold whatever the refused opening left, because it never reduces again.
func (c checkpoint) validateUnstarted() error {
	state := c.State

	empty := c.Base == 0 && state.ReducedThrough == 0 && state.SuppressedRetransmissions == 0 &&
		state.Foreground == nil && len(state.Turns) == 0 && len(state.Activities) == 0 &&
		len(state.Actions) == 0 && len(c.Frames) == 0 && len(c.TurnSeen) == 0 &&
		len(c.ActivitySeen) == 0 && c.BlockedCycle == "" && len(c.ActionCycle) == 0 && len(c.Retired) == 0
	if !empty {
		return errors.New("lifecycle checkpoint carries a projection for an unstarted reduction")
	}

	return nil
}

// validateFrames requires one digest for every sequence the reduction covers.
func (c checkpoint) validateFrames() (map[uint64][sha256.Size]byte, error) {
	frames := make(map[uint64][sha256.Size]byte, len(c.Frames))

	for _, frame := range c.Frames {
		if len(frame.Digest) != sha256.Size {
			return nil, errors.New("lifecycle checkpoint carries a frame digest of the wrong size")
		}

		if !c.Started || frame.Sequence < c.Base || frame.Sequence > c.State.ReducedThrough {
			return nil, errors.New("lifecycle checkpoint carries a frame outside the reduced range")
		}

		if _, duplicate := frames[frame.Sequence]; duplicate {
			return nil, errors.New("lifecycle checkpoint carries one sequence twice")
		}

		frames[frame.Sequence] = [sha256.Size]byte(frame.Digest)
	}

	if c.Started && uint64(len(frames)) != c.State.ReducedThrough-c.Base+1 {
		return nil, errors.New("lifecycle checkpoint does not carry every reduced frame")
	}

	return frames, nil
}

func negotiatedEqual(left, right Negotiated) bool {
	return left.Version == right.Version &&
		left.UpdatesOutsidePrompt == right.UpdatesOutsidePrompt &&
		slices.Equal(left.ActivityKinds, right.ActivityKinds)
}

// sortedKeys renders a set as a sorted, never-null list.
func sortedKeys(set map[string]struct{}) []string {
	keys := slices.AppendSeq(make([]string, 0, len(set)), maps.Keys(set))
	slices.Sort(keys)

	return keys
}

func encodeCheckpointState(state State) checkpointState {
	encoded := checkpointState{
		StreamID:                  state.StreamID,
		ReducedThrough:            state.ReducedThrough,
		SuppressedRetransmissions: state.SuppressedRetransmissions,
		Turns:                     make([]checkpointTurn, 0, len(state.Turns)),
		Activities:                make([]checkpointActivity, 0, len(state.Activities)),
		Actions:                   make([]checkpointAction, 0, len(state.Actions)),
		Closed:                    state.Closed,
	}
	if state.Foreground != nil {
		foreground := checkpointForeground(*state.Foreground)
		encoded.Foreground = &foreground
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

	for index := range state.Actions {
		encoded.Actions = append(encoded.Actions, checkpointAction(state.Actions[index]))
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
		Actions:                   make([]ActionRecord, 0, len(encoded.Actions)),
		Closed:                    encoded.Closed,
	}
	if encoded.Foreground != nil {
		foreground := Foreground(*encoded.Foreground)
		state.Foreground = &foreground
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

	for index := range encoded.Actions {
		state.Actions = append(state.Actions, ActionRecord(encoded.Actions[index]))
	}

	return state
}
