package lifecycle

import "encoding/json"

// stream is one incarnation's ordered emitter. It claims a sequence before
// delivery is attempted, so a lost or refused event leaves a detectable gap
// rather than a silently contiguous stream, and it reduces every event through
// the same reducer the fixture battery drives, so a stream a sibling could not
// support fails at the point of emission instead of at its consumers.
type stream struct {
	id       string
	reducer  *Reducer
	sequence uint64
}

// newStream opens an incarnation identified by id. The identity names one
// native lifecycle source lifetime: it never rotates while that source
// survives, and it never outlives it.
func newStream(id string, negotiated Negotiated) *stream {
	return &stream{id: id, reducer: NewReducer(Options{Negotiated: negotiated})}
}

// fence ends the incarnation by recording its close on the reducer that judges
// every emission. A fenced stream is terminal: nothing more may be emitted on
// it, and native source replacement opens a new incarnation with a new identity
// and a fresh snapshot.
func (s *stream) fence() { s.reducer.Close() }

func (s *stream) fenced() bool { return s.reducer.state.Closed }

// emit claims the next sequence, validates and reduces the notification the
// envelope will ride, and returns the envelope for that notification's _meta.
// A refused event is never handed back and its sequence stays consumed, which
// is exactly the detectable gap the ordering rule wants.
//
// The validation runs on the rendered bytes rather than on the event value: the
// claim being made is that what goes on the wire is well formed, and only the
// consumer's own path of render, decode, and reduce can prove it.
func (s *stream) emit(event Event) (map[string]any, error) {
	s.sequence++

	envelope := map[string]any{
		fieldVersion:  Version,
		fieldStreamID: s.id,
		fieldSequence: s.sequence,
		fieldEvent:    encodeEvent(event),
	}

	// A rendered envelope holds only JSON-safe values, so a payload this step
	// could not produce fails the decode below as a malformed envelope rather
	// than escaping as an untyped error.
	params, _ := json.Marshal(map[string]any{
		metaField:   map[string]any{MetaKey: envelope},
		updateField: map[string]any{sessionUpdateField: string(CarrierSessionInfo)},
	})

	delivery, err := DecodeSessionUpdate(params, s.reducer.Negotiated())
	if err != nil {
		return nil, err
	}

	if err := s.reducer.Reduce(delivery); err != nil {
		return nil, err
	}

	return envelope, nil
}

// snapshotEvent opens a stream from the whole state a sibling can state
// truthfully. The native source is idle when claimed, so the nonterminal sets
// are empty.
func snapshotEvent(cycleID string) Event {
	return Event{Type: EventSnapshot, Snapshot: &Snapshot{
		Foreground: Foreground{State: ForegroundIdle, CycleID: cycleID},
	}}
}

// acceptedEvent records that the native dispatcher took durable ownership of a
// submitted frame. The submission identity is echoed verbatim from the prompt's
// correlation value.
func acceptedEvent(submission Submission, turnID string) Event {
	return Event{Type: EventPromptAccepted, PromptAccepted: &PromptAccepted{
		SubmissionID: submission.SubmissionID,
		ClientNonce:  submission.ClientNonce,
		TurnID:       turnID,
		RunID:        submission.RunID,
	}}
}

// transitionEvent reports a foreground transition from the exact structured
// native cause that opened it.
func transitionEvent(state ForegroundState, cycleID, turnID string, cause Cause) Event {
	return Event{Type: EventStateUpdate, State: &StateTransition{
		State:   state,
		CycleID: cycleID,
		TurnID:  turnID,
		Cause:   cause,
	}}
}

// idleEvent ends a prompt- or agent-origin cycle without changing the origin
// established when that cycle opened.
func idleEvent(cycleID, turnID string, cause Cause, stopReason string, outcome Outcome) Event {
	return Event{Type: EventStateUpdate, State: &StateTransition{
		State:      ForegroundIdle,
		CycleID:    cycleID,
		TurnID:     turnID,
		Cause:      cause,
		StopReason: stopReason,
		Outcome:    outcome,
	}}
}

// actionEvent reports one permission or elicitation's first sight or later state.
func actionEvent(update ActionUpdate) Event {
	return Event{Type: EventActionUpdate, Action: &update}
}

// pendingAction builds one action's first sight.
func pendingAction(actionID string, kind ActionKind, owner Owner, blocksForeground bool) ActionUpdate {
	return ActionUpdate{
		ActionID:         actionID,
		Kind:             kind,
		State:            ActionPending,
		Owner:            owner,
		BlocksForeground: &blocksForeground,
	}
}

// resolvedAction builds one action's terminal patch.
func resolvedAction(actionID string, state ActionState) ActionUpdate {
	return ActionUpdate{ActionID: actionID, State: state}
}

// encodeEvent renders one event for the wire. Every member the contract fixes is
// written from the decoded value, so the emitter and the decoder cannot drift.
func encodeEvent(event Event) map[string]any {
	switch event.Type {
	case EventSnapshot:
		return encodeSnapshot(*event.Snapshot)
	case EventPromptAccepted:
		return withOptional(map[string]any{
			fieldType:         string(EventPromptAccepted),
			fieldSubmissionID: event.PromptAccepted.SubmissionID,
			fieldClientNonce:  event.PromptAccepted.ClientNonce,
			fieldTurnID:       event.PromptAccepted.TurnID,
		}, fieldRunID, event.PromptAccepted.RunID)
	case EventStateUpdate:
		return encodeTransition(*event.State)
	case EventActivityUpdate:
		return map[string]any{
			fieldType:     string(EventActivityUpdate),
			fieldActivity: encodeActivity(*event.Activity),
		}
	default:
		return map[string]any{
			fieldType:   string(EventActionUpdate),
			fieldAction: encodeAction(*event.Action),
		}
	}
}

// encodeSnapshot renders the whole-state assertion. The nonterminal sets are always
// present as arrays, and the foreground names its turn and that turn's origin
// exactly while one is open.
func encodeSnapshot(snapshot Snapshot) map[string]any {
	foreground := map[string]any{
		fieldState:   string(snapshot.Foreground.State),
		fieldCycleID: snapshot.Foreground.CycleID,
	}
	withOptional(foreground, fieldTurnID, snapshot.Foreground.TurnID)
	withOptional(foreground, fieldOrigin, string(snapshot.Foreground.Origin))

	activities := make([]any, 0, len(snapshot.Activities))
	for index := range snapshot.Activities {
		activities = append(activities, encodeActivity(snapshot.Activities[index]))
	}

	actions := make([]any, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		actions = append(actions, encodeAction(action))
	}

	return map[string]any{
		fieldType:       string(EventSnapshot),
		fieldForeground: foreground,
		fieldActivities: activities,
		fieldActions:    actions,
	}
}

func encodeTransition(transition StateTransition) map[string]any {
	encoded := map[string]any{
		fieldType:    string(EventStateUpdate),
		fieldState:   string(transition.State),
		fieldCycleID: transition.CycleID,
		fieldCause:   string(transition.Cause),
	}
	withOptional(encoded, fieldTurnID, transition.TurnID)
	withOptional(encoded, fieldStopReason, transition.StopReason)
	withOptional(encoded, fieldOutcome, string(transition.Outcome))

	return encoded
}

func encodeActivity(activity ActivityUpdate) map[string]any {
	encoded := map[string]any{
		fieldActivityID: activity.ActivityID,
		fieldState:      string(activity.State),
	}
	withOptional(encoded, fieldKind, string(activity.Kind))
	withOptional(encoded, fieldCause, string(activity.Cause))
	withOptional(encoded, fieldOriginTurnID, activity.OriginTurnID)
	withOptional(encoded, fieldParentID, activity.ParentID)
	withOptional(encoded, fieldToolCallID, activity.ToolCallID)
	withOptional(encoded, fieldRunID, activity.RunID)

	if activity.Progress != nil {
		encoded[fieldProgress] = activity.Progress
	}

	return encoded
}

// encodeAction renders one action. A later patch restates no immutable member, so
// the members a first sight fixes are written only when they are present.
func encodeAction(action ActionUpdate) map[string]any {
	encoded := map[string]any{
		fieldActionID: action.ActionID,
		fieldState:    string(action.State),
	}
	withOptional(encoded, fieldKind, string(action.Kind))
	withOptional(encoded, fieldRunID, action.RunID)

	if action.Owner.ID != "" {
		encoded[fieldOwner] = map[string]any{
			fieldType: string(action.Owner.Type),
			fieldID:   action.Owner.ID,
		}
	}

	if action.BlocksForeground != nil {
		encoded[fieldBlocksForeground] = *action.BlocksForeground
	}

	return encoded
}

// withOptional adds a member only when it has a value. An optional member is omitted
// rather than emitted empty, because an empty opaque identifier fails closed on the
// reading side.
func withOptional(encoded map[string]any, key, value string) map[string]any {
	if value != "" {
		encoded[key] = value
	}

	return encoded
}
