package lifecycle

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
)

// ErrNoEnvelope reports that a frame carries no lifecycle envelope. It is not a
// violation: ordinary content rides its own surfaces without one.
var ErrNoEnvelope = errors.New("frame carries no lifecycle envelope")

// metaField is the ACP notification member every reserved value rides under.
const metaField = "_meta"

// updateField is the session/update member carrying the update variant.
const updateField = "update"

// sessionUpdateField discriminates an ACP session update variant.
const sessionUpdateField = "sessionUpdate"

// DecodeSessionUpdate reads the lifecycle delivery carried by one session/update
// notification payload. It reports ErrNoEnvelope for a frame with no extension
// value, and a *ViolationError naming the closed token for every frame it refuses.
func DecodeSessionUpdate(params json.RawMessage, negotiated Negotiated) (Delivery, error) {
	frame, readable := decodeValue(params)
	if !readable {
		return Delivery{}, violation(ViolationMalformedEnvelope, "", 0, "the notification is not decodable JSON")
	}

	notification, ok := jsonObject(params)
	if !ok {
		return Delivery{}, violation(ViolationMalformedEnvelope, "", 0, "the notification is not an object")
	}

	meta, ok := jsonObject(notification[metaField])
	if !ok {
		return Delivery{}, ErrNoEnvelope
	}

	raw, present := meta[MetaKey]
	if !present {
		return Delivery{}, ErrNoEnvelope
	}

	if !negotiated.Present() {
		return Delivery{}, violation(ViolationUnnegotiatedFact, "", 0, "the answer omitted the lifecycle key")
	}

	dec := &decoder{negotiated: negotiated}

	event := dec.envelope(raw)
	if dec.err != nil {
		return Delivery{}, dec.err
	}

	if carrier := carrierClassForSessionUpdate(notification[updateField]); carrier != CarrierSessionInfo {
		return Delivery{}, violation(ViolationIllegalCarrier, dec.streamID, dec.sequence,
			"only the identity-only "+sessionUpdateField+" "+string(CarrierSessionInfo)+" carries an envelope")
	}

	return Delivery{
		StreamID: dec.streamID,
		Sequence: dec.sequence,
		Carrier:  CarrierSessionInfo,
		Event:    event,
		Frame:    frame,
	}, nil
}

// carrierClassForSessionUpdate classifies one ACP session update. The
// identity-only session_info_update is the only eligible carrier: it sets no title
// and no updatedAt, so carrying an envelope mutates no state.
func carrierClassForSessionUpdate(update json.RawMessage) CarrierClass {
	fields, ok := jsonObject(update)
	if !ok {
		return CarrierUnknown
	}

	kind, ok := jsonString(fields[sessionUpdateField])
	if !ok {
		return CarrierUnknown
	}

	if kind != string(CarrierSessionInfo) {
		return CarrierIneligible
	}

	_, titled := fields["title"]
	_, stamped := fields["updatedAt"]

	if titled || stamped {
		return CarrierIneligible
	}

	return CarrierSessionInfo
}

// decoder reads one envelope. It records the first refusal and keeps whatever
// ordering identity it managed to decode, so a refusal names the frame it refused.
type decoder struct {
	negotiated Negotiated
	streamID   string
	sequence   uint64
	err        *ViolationError
}

func (d *decoder) fail(kind ViolationKind, detail string) {
	if d.err == nil {
		d.err = violation(kind, d.streamID, d.sequence, detail)
	}
}

// envelope reads the four fixed members and the event they carry. Nothing here
// consults stream state: structural validity is checked before ordering.
func (d *decoder) envelope(raw json.RawMessage) Event {
	fields, ok := jsonObject(raw)
	if !ok {
		d.fail(ViolationIllegalCarrier, "the extension value is not an object")

		return Event{}
	}

	d.streamID = d.identifier(fields, fieldStreamID, true)
	d.sequence = d.positive(fields)
	d.version(fields)
	d.known(fields, "envelope", fieldVersion, fieldStreamID, fieldSequence, fieldEvent)

	event, ok := jsonObject(fields[fieldEvent])
	if !ok {
		d.fail(ViolationMalformedEnvelope, "event is missing or is not an object")

		return Event{}
	}

	return d.event(event)
}

// version reads the single negotiated integer every envelope and correlation value
// carries.
func (d *decoder) version(fields map[string]json.RawMessage) {
	raw, present := fields[fieldVersion]
	if !present {
		d.fail(ViolationMalformedEnvelope, "version is missing")

		return
	}

	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		d.fail(ViolationMalformedEnvelope, "version is not an integer")

		return
	}

	if !d.negotiated.SupportsVersion(version) {
		d.fail(ViolationUnsupportedVersion, "version "+strconv.Itoa(version)+" is outside the negotiated set")
	}
}

func (d *decoder) positive(fields map[string]json.RawMessage) uint64 {
	raw, present := fields[fieldSequence]
	if !present {
		d.fail(ViolationMalformedEnvelope, "sequence is missing")

		return 0
	}

	var sequence uint64
	if err := json.Unmarshal(raw, &sequence); err != nil || sequence == 0 {
		d.fail(ViolationMalformedEnvelope, "sequence is not a positive integer")

		return 0
	}

	return sequence
}

func (d *decoder) event(fields map[string]json.RawMessage) Event {
	name, ok := jsonString(fields[fieldType])
	if !ok {
		d.fail(ViolationMalformedEnvelope, "event type is missing")

		return Event{}
	}

	switch EventType(name) {
	case EventSnapshot:
		return d.snapshot(fields)
	case EventPromptAccepted:
		return d.promptAccepted(fields)
	case EventStateUpdate:
		return d.stateUpdate(fields)
	case EventActivityUpdate:
		return d.activityUpdate(fields)
	case EventActionUpdate:
		return d.actionUpdate(fields)
	default:
		d.fail(ViolationUnknownEventType, "event type "+name)

		return Event{}
	}
}

func (d *decoder) snapshot(fields map[string]json.RawMessage) Event {
	d.known(fields, string(EventSnapshot), fieldType, fieldForeground, fieldActivities, fieldActions)

	snapshot := &Snapshot{Foreground: d.foreground(fields[fieldForeground])}
	for _, raw := range d.array(fields, fieldActivities) {
		snapshot.Activities = append(snapshot.Activities, d.activity(raw))
	}

	for _, raw := range d.array(fields, fieldActions) {
		snapshot.Actions = append(snapshot.Actions, d.action(raw))
	}

	return Event{Type: EventSnapshot, Snapshot: snapshot}
}

// foreground reads the snapshot's foreground object. An idle foreground names no
// turn, and a running or requires-action one names both its turn and that turn's
// origin, because a live foreground with no turn to own it asserts a cycle no
// resumed projection could ever attach a reference to.
func (d *decoder) foreground(raw json.RawMessage) Foreground {
	fields, ok := jsonObject(raw)
	if !ok {
		d.fail(ViolationMalformedEnvelope, "foreground is missing")

		return Foreground{}
	}

	d.known(fields, fieldForeground, fieldState, fieldCycleID, fieldTurnID, fieldOrigin)

	foreground := Foreground{
		State:   ForegroundState(d.identifier(fields, fieldState, true)),
		CycleID: d.identifier(fields, fieldCycleID, true),
		TurnID:  d.identifier(fields, fieldTurnID, false),
		Origin:  Cause(d.identifier(fields, fieldOrigin, false)),
	}

	if detail := foregroundDefect(foreground); detail != "" {
		d.fail(ViolationMalformedEnvelope, detail)
	}

	return foreground
}

// foregroundDefect reports why a snapshot foreground is structurally invalid, or
// the empty string when it is not. Both the decoder and the reducer consult it.
func foregroundDefect(foreground Foreground) string {
	switch {
	case !foreground.State.Valid():
		return "foreground state " + string(foreground.State)
	case foreground.CycleID == "":
		return "the snapshot's foreground is incomplete"
	case foreground.State == ForegroundIdle && foreground.TurnID != "":
		return "an idle foreground reports no turn"
	case foreground.State != ForegroundIdle && foreground.TurnID == "":
		return "a " + string(foreground.State) + " foreground names the turn that owns it"
	case (foreground.TurnID == "") != (foreground.Origin == ""):
		return "foreground origin is present exactly while a turn is"
	case foreground.Origin != "" && foreground.Origin != CauseSubmission && foreground.Origin != CauseActivity:
		return "foreground origin " + string(foreground.Origin)
	default:
		return ""
	}
}

func (d *decoder) promptAccepted(fields map[string]json.RawMessage) Event {
	d.known(fields, string(EventPromptAccepted), fieldType, fieldSubmissionID, fieldClientNonce, fieldTurnID, fieldRunID)

	return Event{Type: EventPromptAccepted, PromptAccepted: &PromptAccepted{
		SubmissionID: d.identifier(fields, fieldSubmissionID, true),
		ClientNonce:  d.identifier(fields, fieldClientNonce, true),
		TurnID:       d.identifier(fields, fieldTurnID, true),
		RunID:        d.identifier(fields, fieldRunID, false),
	}}
}

func (d *decoder) stateUpdate(fields map[string]json.RawMessage) Event {
	d.known(fields, string(EventStateUpdate), fieldType, fieldState, fieldCycleID, fieldTurnID,
		fieldCause, fieldStopReason, fieldOutcome)

	transition := &StateTransition{
		State:      ForegroundState(d.identifier(fields, fieldState, true)),
		CycleID:    d.identifier(fields, fieldCycleID, true),
		TurnID:     d.identifier(fields, fieldTurnID, false),
		Cause:      Cause(d.identifier(fields, fieldCause, true)),
		StopReason: d.identifier(fields, fieldStopReason, false),
		Outcome:    Outcome(d.identifier(fields, fieldOutcome, false)),
	}

	nameless := namelessTurnDefect(*transition)

	switch {
	case !transition.State.Valid():
		d.fail(ViolationMalformedEnvelope, "transition state "+string(transition.State))
	case !transition.Cause.Valid():
		d.fail(ViolationMalformedEnvelope, "transition cause "+string(transition.Cause))
	case nameless != "":
		d.fail(ViolationMalformedEnvelope, nameless)
	case transition.State != ForegroundIdle && (transition.StopReason != "" || transition.Outcome != ""):
		d.fail(ViolationMalformedEnvelope, "only an ending transition carries a stop reason and an outcome")
	case transition.StopReason != "" && !validStopReason(transition.StopReason):
		d.fail(ViolationMalformedEnvelope, "stop reason "+transition.StopReason)
	case transition.Outcome != "" && !transition.Outcome.Valid():
		d.fail(ViolationMalformedEnvelope, "outcome "+string(transition.Outcome))
	default:
		if detail := endingIdleDefect(*transition); detail != "" {
			d.fail(ViolationMalformedEnvelope, detail)
		}
	}

	return Event{Type: EventStateUpdate, State: transition}
}

func (d *decoder) activityUpdate(fields map[string]json.RawMessage) Event {
	d.known(fields, string(EventActivityUpdate), fieldType, fieldActivity)

	if _, ok := jsonObject(fields[fieldActivity]); !ok {
		d.fail(ViolationMalformedEnvelope, "activity_update carries no activity")

		return Event{Type: EventActivityUpdate, Activity: &ActivityUpdate{}}
	}

	activity := d.activity(fields[fieldActivity])

	return Event{Type: EventActivityUpdate, Activity: &activity}
}

func (d *decoder) activity(raw json.RawMessage) ActivityUpdate {
	fields, ok := jsonObject(raw)
	if !ok {
		d.fail(ViolationMalformedEnvelope, "activity is not an object")

		return ActivityUpdate{}
	}

	d.known(fields, fieldActivity, fieldActivityID, fieldKind, fieldState, fieldParentID,
		fieldToolCallID, fieldCause, fieldOriginTurnID, fieldRunID, fieldProgress)

	activity := ActivityUpdate{
		ActivityID:   d.identifier(fields, fieldActivityID, true),
		Kind:         ActivityKind(d.identifier(fields, fieldKind, false)),
		State:        ActivityState(d.identifier(fields, fieldState, true)),
		ParentID:     d.identifier(fields, fieldParentID, false),
		ToolCallID:   d.identifier(fields, fieldToolCallID, false),
		Cause:        Cause(d.identifier(fields, fieldCause, false)),
		OriginTurnID: d.identifier(fields, fieldOriginTurnID, false),
		RunID:        d.identifier(fields, fieldRunID, false),
		Progress:     d.progress(fields),
	}

	switch {
	case activity.Kind != "" && !activity.Kind.Valid():
		d.fail(ViolationMalformedEnvelope, "activity kind "+string(activity.Kind))
	case !activity.State.Valid():
		d.fail(ViolationMalformedEnvelope, "activity state "+string(activity.State))
	case activity.Cause != "" && !activity.Cause.Valid():
		d.fail(ViolationMalformedEnvelope, "activity cause "+string(activity.Cause))
	}

	return activity
}

// progress is bounded and must be an object; its members are deliberately never
// validated against the unknown-member rule.
func (d *decoder) progress(fields map[string]json.RawMessage) json.RawMessage {
	raw, present := fields[fieldProgress]
	if !present {
		return nil
	}

	if _, ok := jsonObject(raw); !ok {
		d.fail(ViolationMalformedEnvelope, "activity progress is not an object")

		return nil
	}

	if len(raw) > IdentifierBound {
		d.fail(ViolationMalformedEnvelope, "activity progress exceeds its bound")

		return nil
	}

	return raw
}

func (d *decoder) actionUpdate(fields map[string]json.RawMessage) Event {
	d.known(fields, string(EventActionUpdate), fieldType, fieldAction)

	if _, ok := jsonObject(fields[fieldAction]); !ok {
		d.fail(ViolationMalformedEnvelope, "action_update carries no action")

		return Event{Type: EventActionUpdate, Action: &ActionUpdate{}}
	}

	action := d.action(fields[fieldAction])

	return Event{Type: EventActionUpdate, Action: &action}
}

func (d *decoder) action(raw json.RawMessage) ActionUpdate {
	fields, ok := jsonObject(raw)
	if !ok {
		d.fail(ViolationMalformedEnvelope, "action is not an object")

		return ActionUpdate{}
	}

	d.known(fields, fieldAction, fieldActionID, fieldKind, fieldState, fieldOwner, fieldRunID, fieldBlocksForeground)

	action := ActionUpdate{
		ActionID:         d.identifier(fields, fieldActionID, true),
		Kind:             ActionKind(d.identifier(fields, fieldKind, false)),
		State:            ActionState(d.identifier(fields, fieldState, true)),
		Owner:            d.owner(fields),
		RunID:            d.identifier(fields, fieldRunID, false),
		BlocksForeground: d.boolean(fields, fieldBlocksForeground),
	}

	switch {
	case action.Kind != "" && !action.Kind.Valid():
		d.fail(ViolationMalformedEnvelope, "action kind "+string(action.Kind))
	case !action.State.Valid():
		d.fail(ViolationMalformedEnvelope, "action state "+string(action.State))
	}

	return action
}

func (d *decoder) owner(fields map[string]json.RawMessage) Owner {
	raw, present := fields[fieldOwner]
	if !present {
		return Owner{}
	}

	owned, ok := jsonObject(raw)
	if !ok {
		d.fail(ViolationMalformedEnvelope, "action owner is not an object")

		return Owner{}
	}

	d.known(owned, fieldOwner, fieldType, fieldID)

	owner := Owner{
		Type: OwnerType(d.identifier(owned, fieldType, true)),
		ID:   d.identifier(owned, fieldID, true),
	}
	if !owner.Type.Valid() {
		d.fail(ViolationMalformedEnvelope, "action owner type "+string(owner.Type))
	}

	return owner
}

// identifier reads one opaque string member. An identifier is a correlation
// handle, so it is bounded, and it is never empty: an optional one is omitted
// rather than emptied, so a member present carrying the empty string is malformed
// rather than absent.
func (d *decoder) identifier(fields map[string]json.RawMessage, key string, required bool) string {
	raw, present := fields[key]
	if !present {
		if required {
			d.fail(ViolationMalformedEnvelope, key+" is missing")
		}

		return ""
	}

	value, ok := jsonString(raw)

	switch {
	case !ok:
		d.fail(ViolationMalformedEnvelope, key+" is not a string")
	case value == "":
		d.fail(ViolationMalformedEnvelope, key+" is empty")
	case len(value) > IdentifierBound:
		d.fail(ViolationMalformedEnvelope, key+" exceeds its bound")
	}

	return value
}

// boolean reads one optional boolean member. It reports absence rather than
// false: a member read as false by default makes an omitted one and a stated one
// indistinguishable, which is exactly what an action's first sight may not do with
// blocksForeground.
func (d *decoder) boolean(fields map[string]json.RawMessage, key string) *bool {
	raw, present := fields[key]
	if !present {
		return nil
	}

	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		d.fail(ViolationMalformedEnvelope, key+" is not a boolean")

		return nil
	}

	return &value
}

func (d *decoder) array(fields map[string]json.RawMessage, key string) []json.RawMessage {
	raw, present := fields[key]
	if !present {
		d.fail(ViolationMalformedEnvelope, key+" is missing")

		return nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		d.fail(ViolationMalformedEnvelope, key+" is not an array")

		return nil
	}

	return entries
}

// known refuses any member this contract does not fix. Reducers ignoring
// different unknown members is how a closed shape stops being closed.
func (d *decoder) known(fields map[string]json.RawMessage, object string, allowed ...string) {
	for key := range fields {
		if !slices.Contains(allowed, key) {
			d.fail(ViolationUnknownField, object+" carries unknown member "+key)

			return
		}
	}
}

func jsonObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, false
	}

	return fields, true
}

func jsonString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}

	return value, true
}
