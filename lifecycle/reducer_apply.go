package lifecycle

import "slices"

// applySnapshot opens the stream from a whole-state assertion. A snapshot is taken
// whole or not at all: the entire assertion is judged before any of it is
// projected, so a refused snapshot opens nothing and leaves no half-built
// projection behind.
func (r *Reducer) applySnapshot(delivery Delivery) error {
	snapshot := delivery.Event.Snapshot
	if snapshot == nil {
		return r.fail(delivery, ViolationMalformedEnvelope, "the snapshot payload is missing")
	}

	if err := r.checkSnapshot(delivery, *snapshot); err != nil {
		return err
	}

	r.projectSnapshot(*snapshot)

	return nil
}

// checkSnapshot judges the assertion. A snapshot introduces exactly what it
// names: the turn its foreground reports, the turns its activities name as
// origin, and its own activities. Every reference it makes resolves inside that
// set; an action owner is a reference rather than an introduction.
func (r *Reducer) checkSnapshot(delivery Delivery, snapshot Snapshot) error {
	if detail := foregroundDefect(snapshot.Foreground); detail != "" {
		return r.fail(delivery, ViolationMalformedEnvelope, detail)
	}

	introduced := snapshot.introduces()

	if err := r.checkSnapshotActivities(delivery, snapshot, introduced); err != nil {
		return err
	}

	if err := r.checkSnapshotActions(delivery, snapshot, introduced); err != nil {
		return err
	}

	if snapshot.Foreground.State == ForegroundRequiresAction && !snapshot.carriesBlocker() {
		return r.fail(delivery, ViolationInconsistentForeground,
			"cycle "+snapshot.Foreground.CycleID+" lists no blocking action")
	}

	return nil
}

// checkSnapshotActivities validates the asserted activity set. The set is the
// complete nonterminal one, so an entry that is already terminal asserts as
// current a state that is over, and an id listed twice asserts two current
// states for one entity. Uniqueness is judged per set because activities and
// actions are distinct id spaces.
func (r *Reducer) checkSnapshotActivities(delivery Delivery, snapshot Snapshot, introduced introductions) error {
	listed := make(map[string]struct{}, len(snapshot.Activities))

	for index := range snapshot.Activities {
		activity := &snapshot.Activities[index]

		if _, twice := listed[activity.ActivityID]; twice {
			return r.fail(delivery, ViolationMalformedEnvelope,
				"activity "+activity.ActivityID+" is listed twice")
		}

		listed[activity.ActivityID] = struct{}{}

		if err := r.checkActivityIdentity(delivery, *activity); err != nil {
			return err
		}

		if activity.State.Terminal() {
			return r.fail(delivery, ViolationMalformedEnvelope, "activity "+activity.ActivityID+" is terminal")
		}

		if activity.ParentID != "" && !introduced.activities[activity.ParentID] {
			return r.fail(delivery, ViolationUnknownEntity, "parent activity "+activity.ParentID+" is not introduced")
		}
	}

	return nil
}

func (r *Reducer) checkSnapshotActions(delivery Delivery, snapshot Snapshot, introduced introductions) error {
	listed := make(map[string]struct{}, len(snapshot.Actions))

	for _, action := range snapshot.Actions {
		if _, twice := listed[action.ActionID]; twice {
			return r.fail(delivery, ViolationMalformedEnvelope, "action "+action.ActionID+" is listed twice")
		}

		listed[action.ActionID] = struct{}{}

		if err := r.checkActionIdentity(delivery, action); err != nil {
			return err
		}

		if action.State.Terminal() {
			return r.fail(delivery, ViolationMalformedEnvelope, "action "+action.ActionID+" is terminal")
		}

		if !introduced.holds(action.Owner) {
			return r.fail(delivery, ViolationUnknownEntity,
				"owner "+string(action.Owner.Type)+" "+action.Owner.ID+" is not introduced")
		}
	}

	return nil
}

// projectSnapshot installs the validated assertion. A snapshot resuming mid-turn
// projects that turn as open with the origin it reported, because a resumed stream
// whose foreground names a turn nothing later opened would leave every reference
// to it unresolvable.
func (r *Reducer) projectSnapshot(snapshot Snapshot) {
	foreground := snapshot.Foreground
	r.state.Foreground = &foreground

	if foreground.TurnID != "" {
		r.state.Turns = append(r.state.Turns, TurnRecord{
			TurnID:  foreground.TurnID,
			Origin:  foreground.Origin,
			CycleID: foreground.CycleID,
		})
		r.turnSeen[foreground.TurnID] = struct{}{}
	}

	for index := range snapshot.Activities {
		r.turnSeen[snapshot.Activities[index].OriginTurnID] = struct{}{}
		r.recordActivity(snapshot.Activities[index])
	}

	for _, action := range snapshot.Actions {
		r.recordAction(action)
	}
}

// introductions is the identity set a snapshot brings into existence.
type introductions struct {
	turns      map[string]bool
	activities map[string]bool
}

func (s Snapshot) introduces() introductions {
	introduced := introductions{turns: map[string]bool{}, activities: map[string]bool{}}
	if s.Foreground.TurnID != "" {
		introduced.turns[s.Foreground.TurnID] = true
	}

	for index := range s.Activities {
		introduced.turns[s.Activities[index].OriginTurnID] = true
		introduced.activities[s.Activities[index].ActivityID] = true
	}

	return introduced
}

func (i introductions) holds(owner Owner) bool {
	if owner.Type == OwnerActivity {
		return i.activities[owner.ID]
	}

	return i.turns[owner.ID]
}

// carriesBlocker reports whether the snapshot's own action set explains a
// requires_action foreground. The set is complete, so a blocker it does not list
// does not exist.
func (s Snapshot) carriesBlocker() bool {
	return slices.ContainsFunc(s.Actions, blocksForeground)
}

// applyPromptAccepted opens a prompt-origin turn. Acceptance introduces its turn,
// so it happens once per turn: a second acceptance of a turn the stream already
// introduced changes that turn's identity rather than reporting a new one.
func (r *Reducer) applyPromptAccepted(delivery Delivery) error {
	accepted := delivery.Event.PromptAccepted
	if accepted == nil {
		return r.fail(delivery, ViolationMalformedEnvelope, "the acceptance payload is missing")
	}

	index := r.turnIndex(accepted.TurnID)

	switch {
	case index >= 0 && r.state.Turns[index].Terminal:
		return r.fail(delivery, ViolationPostTerminalMutation, "turn "+accepted.TurnID+" is terminal")
	case r.turnKnown(accepted.TurnID):
		return r.fail(delivery, ViolationImmutableIdentityChange,
			"turn "+accepted.TurnID+" was already introduced")
	}

	r.state.Turns = append(r.state.Turns, TurnRecord{
		TurnID:       accepted.TurnID,
		Origin:       CauseSubmission,
		SubmissionID: accepted.SubmissionID,
		ClientNonce:  accepted.ClientNonce,
		RunID:        accepted.RunID,
	})
	r.turnSeen[accepted.TurnID] = struct{}{}

	return nil
}

func (r *Reducer) applyStateUpdate(delivery Delivery) error {
	transition := delivery.Event.State
	if transition == nil {
		return r.fail(delivery, ViolationMalformedEnvelope, "the transition payload is missing")
	}

	if detail := namelessTurnDefect(*transition); detail != "" {
		return r.fail(delivery, ViolationMalformedEnvelope, detail)
	}

	if detail := endingIdleDefect(*transition); detail != "" {
		return r.fail(delivery, ViolationMalformedEnvelope, detail)
	}

	if err := r.checkBlockedCycle(delivery, *transition); err != nil {
		return err
	}

	if transition.State == ForegroundIdle {
		return r.applyIdle(delivery, *transition)
	}

	return r.applyLive(delivery, *transition)
}

// checkBlockedCycle enforces both halves of the blocking-action rule: a
// requires-action transition names a cycle something is actually blocking, and a
// cycle a blocking action stopped neither leaves that state nor ends while any
// action blocking it is still nonterminal.
func (r *Reducer) checkBlockedCycle(delivery Delivery, transition StateTransition) error {
	if transition.State == ForegroundRequiresAction {
		if !r.blocked(transition.CycleID) {
			return r.fail(delivery, ViolationInconsistentForeground,
				"cycle "+transition.CycleID+" has no outstanding blocking action")
		}

		if r.blockedCycle == transition.CycleID {
			r.blockedCycle = ""
		}

		return nil
	}

	if r.blocked(transition.CycleID) || r.blockedCycle == transition.CycleID {
		return r.fail(delivery, ViolationInconsistentForeground,
			"cycle "+transition.CycleID+" is blocked and reported "+string(transition.State))
	}

	return nil
}

// blocked reports whether an action that stopped this cycle is still nonterminal.
func (r *Reducer) blocked(cycleID string) bool {
	for _, action := range r.state.Actions {
		if action.BlocksForeground && !action.State.Terminal() && r.actionCycle[action.ActionID] == cycleID {
			return true
		}
	}

	return false
}

// applyLive reduces a running or requires-action transition. Exactly two events
// open a turn, and this is the second: an activity-caused running transition
// bearing a turn the stream has not introduced opens an agent-origin turn. Every
// other transition names a turn the stream already opened.
func (r *Reducer) applyLive(delivery Delivery, transition StateTransition) error {
	index := r.turnIndex(transition.TurnID)

	switch {
	case index >= 0 && r.state.Turns[index].Terminal:
		return r.fail(delivery, ViolationPostTerminalMutation, "turn "+transition.TurnID+" is terminal")
	case index < 0:
		if transition.Cause != CauseActivity || transition.State != ForegroundRunning {
			return r.fail(delivery, ViolationUnknownEntity, "turn "+transition.TurnID+" was never opened")
		}

		r.state.Turns = append(r.state.Turns, TurnRecord{TurnID: transition.TurnID, Origin: CauseActivity})
		r.turnSeen[transition.TurnID] = struct{}{}

		index = len(r.state.Turns) - 1
	}

	r.state.Turns[index].CycleID = transition.CycleID
	r.state.Foreground = &Foreground{
		State:   transition.State,
		CycleID: transition.CycleID,
		TurnID:  transition.TurnID,
	}

	return nil
}

// applyIdle ends a foreground cycle. An ending transition never opens a turn, so
// it must name one the stream already introduced.
func (r *Reducer) applyIdle(delivery Delivery, transition StateTransition) error {
	index := r.turnIndex(transition.TurnID)

	switch {
	case transition.TurnID == "":
		r.state.Foreground = &Foreground{State: ForegroundIdle, CycleID: transition.CycleID}

		return nil
	case index >= 0 && r.state.Turns[index].Terminal:
		return r.fail(delivery, ViolationPostTerminalMutation, "turn "+transition.TurnID+" is terminal")
	case index < 0:
		return r.fail(delivery, ViolationUnknownEntity, "turn "+transition.TurnID+" was never opened")
	}

	turn := &r.state.Turns[index]
	turn.Terminal = true
	turn.StopReason = transition.StopReason
	turn.Outcome = transition.Outcome
	turn.CycleID = transition.CycleID
	r.state.Foreground = &Foreground{State: ForegroundIdle, CycleID: transition.CycleID}

	return nil
}

func (r *Reducer) applyActivityUpdate(delivery Delivery) error {
	update := delivery.Event.Activity
	if update == nil {
		return r.fail(delivery, ViolationMalformedEnvelope, "the activity payload is missing")
	}

	if r.activityIndex(update.ActivityID) >= 0 {
		return r.patchActivity(delivery, *update)
	}

	if err := r.checkActivityIdentity(delivery, *update); err != nil {
		return err
	}

	if err := r.checkActivityReferences(delivery, *update); err != nil {
		return err
	}

	if err := r.checkActivityParent(delivery, update.ActivityID, update.ParentID); err != nil {
		return err
	}

	r.recordActivity(*update)

	return nil
}

// checkActivityReferences resolves a first sight's references. A parent with no
// prior first sight or an origin turn the stream never opened would leave
// parentage, ownership, and terminal ordering unenforceable.
func (r *Reducer) checkActivityReferences(delivery Delivery, update ActivityUpdate) error {
	if !r.turnKnown(update.OriginTurnID) {
		return r.fail(delivery, ViolationUnknownEntity, "origin turn "+update.OriginTurnID+" was never opened")
	}

	if update.ParentID != "" && !r.activityKnown(update.ParentID) {
		return r.fail(delivery, ViolationUnknownEntity, "parent activity "+update.ParentID+" was never seen")
	}

	return nil
}

// checkActivityIdentity validates an activity's first sight, when every immutable
// identity field must be present and the kind must be one the answer proved.
func (r *Reducer) checkActivityIdentity(delivery Delivery, update ActivityUpdate) error {
	switch {
	case update.Kind == "" || update.Cause == "" || update.OriginTurnID == "":
		return r.fail(delivery, ViolationImmutableIdentityChange,
			"activity "+update.ActivityID+" states an incomplete identity")
	case !r.negotiated.DeclaresActivityKind(update.Kind):
		return r.fail(delivery, ViolationUnnegotiatedFact, "activity kind "+string(update.Kind))
	}

	return nil
}

func (r *Reducer) recordActivity(update ActivityUpdate) {
	r.state.Activities = append(r.state.Activities, ActivityRecord(update))
	r.activitySeen[update.ActivityID] = struct{}{}
}

// checkActivityParent validates parentage: a parent this stream introduced must
// still be nonterminal when it gains a child.
func (r *Reducer) checkActivityParent(delivery Delivery, activityID, parentID string) error {
	index := r.activityIndex(parentID)
	if parentID == "" || index < 0 || activityID == parentID {
		return nil
	}

	if r.state.Activities[index].State.Terminal() {
		return r.fail(delivery, ViolationChildAfterParentTerminal, "parent "+parentID+" is terminal")
	}

	return nil
}

// patchActivity applies a later update, which may change only state and progress.
// A restated immutable field is permitted only with its first-sight value, and
// changes nothing.
//
// A terminal activity is judged first and separately: it admits nothing but a
// no-op restatement, and that restatement is suppressed rather than applied.
func (r *Reducer) patchActivity(delivery Delivery, update ActivityUpdate) error {
	index := r.activityIndex(update.ActivityID)

	existing := r.state.Activities[index]
	if existing.State.Terminal() {
		if detail := terminalActivityDifference(existing, update); detail != "" {
			return r.fail(delivery, ViolationPostTerminalMutation, detail)
		}

		return nil
	}

	if detail := immutableActivityConflict(existing, update); detail != "" {
		return r.fail(delivery, ViolationImmutableIdentityChange, detail)
	}

	if update.State.Terminal() {
		if err := r.checkDescendantsTerminal(delivery, existing.ActivityID); err != nil {
			return err
		}
	}

	r.state.Activities[index].State = update.State

	if update.Progress != nil {
		r.state.Activities[index].Progress = update.Progress
	}

	return nil
}

// terminalActivityDifference reports the first member a restatement carries that
// differs from the reduced terminal record, or the empty string when the patch
// carries no difference at all. Only the members the event states are compared;
// an omitted member restates nothing.
func terminalActivityDifference(existing ActivityRecord, update ActivityUpdate) string {
	if detail := immutableActivityConflict(existing, update); detail != "" {
		return detail
	}

	switch {
	case update.State != existing.State:
		return "activity " + update.ActivityID + " is terminal"
	case update.Progress != nil && !rawEqual(update.Progress, existing.Progress):
		return "activity " + update.ActivityID + " changed progress after it finished"
	default:
		return ""
	}
}

// checkDescendantsTerminal refuses a parent that would terminalize while part of
// the subtree it claims to have finished is still live.
func (r *Reducer) checkDescendantsTerminal(delivery Delivery, activityID string) error {
	for index := range r.state.Activities {
		if child := &r.state.Activities[index]; child.ParentID == activityID && !child.State.Terminal() {
			return r.fail(delivery, ViolationParentTerminalBeforeChild, "child "+child.ActivityID+" is live")
		}
	}

	for _, action := range r.state.Actions {
		if action.Owner.Type == OwnerActivity && action.Owner.ID == activityID && !action.State.Terminal() {
			return r.fail(delivery, ViolationParentTerminalBeforeChild, "action "+action.ActionID+" is unresolved")
		}
	}

	return nil
}

func immutableActivityConflict(existing ActivityRecord, update ActivityUpdate) string {
	switch {
	case update.Kind != "" && update.Kind != existing.Kind:
		return "activity " + update.ActivityID + " changed kind"
	case update.ParentID != "" && update.ParentID != existing.ParentID:
		return "activity " + update.ActivityID + " changed parent"
	case update.ToolCallID != "" && update.ToolCallID != existing.ToolCallID:
		return "activity " + update.ActivityID + " changed tool link"
	case update.Cause != "" && update.Cause != existing.Cause:
		return "activity " + update.ActivityID + " changed cause"
	case update.OriginTurnID != "" && update.OriginTurnID != existing.OriginTurnID:
		return "activity " + update.ActivityID + " changed origin turn"
	case update.RunID != "" && update.RunID != existing.RunID:
		return "activity " + update.ActivityID + " changed ownership root"
	default:
		return ""
	}
}

func (r *Reducer) applyActionUpdate(delivery Delivery) error {
	update := delivery.Event.Action
	if update == nil {
		return r.fail(delivery, ViolationMalformedEnvelope, "the action payload is missing")
	}

	if r.actionIndex(update.ActionID) >= 0 {
		return r.patchAction(delivery, *update)
	}

	if err := r.checkActionIdentity(delivery, *update); err != nil {
		return err
	}

	if err := r.checkActionOwner(delivery, *update); err != nil {
		return err
	}

	r.recordAction(*update)

	return nil
}

// checkActionIdentity validates an action's first sight, when every member that
// fixes what the action is must be present. blocksForeground read as false by
// default would silently demote a blocking request to a background one.
func (r *Reducer) checkActionIdentity(delivery Delivery, update ActionUpdate) error {
	if update.Kind == "" || update.Owner.ID == "" || update.BlocksForeground == nil {
		return r.fail(delivery, ViolationMalformedEnvelope,
			"action "+update.ActionID+" states an incomplete first sight")
	}

	return nil
}

// blocksForeground reports a stated blocking claim. An omitted member states
// nothing, which is why only a first sight is required to carry one.
func blocksForeground(update ActionUpdate) bool {
	return update.BlocksForeground != nil && *update.BlocksForeground
}

// checkActionOwner resolves the entity an action is owned by. An action hung off a
// turn or activity the stream never introduced could never be attributed.
func (r *Reducer) checkActionOwner(delivery Delivery, update ActionUpdate) error {
	known := r.turnKnown(update.Owner.ID)
	if update.Owner.Type == OwnerActivity {
		known = r.activityKnown(update.Owner.ID)
	}

	if !known {
		return r.fail(delivery, ViolationUnknownEntity,
			"owner "+string(update.Owner.Type)+" "+update.Owner.ID+" was never opened")
	}

	return nil
}

func (r *Reducer) recordAction(update ActionUpdate) {
	r.state.Actions = append(r.state.Actions, ActionRecord{
		ActionID:         update.ActionID,
		Kind:             update.Kind,
		State:            update.State,
		Owner:            update.Owner,
		RunID:            update.RunID,
		BlocksForeground: *update.BlocksForeground,
	})

	if update.State.Terminal() {
		return
	}

	r.blockForeground(update)
}

// blockForeground records the cycle a blocking action stopped and the transition
// that cycle now owes. A blocker blocks the cycle current at its first sight, and
// it never moves the foreground by itself.
func (r *Reducer) blockForeground(update ActionUpdate) {
	if !blocksForeground(update) || r.state.Foreground == nil {
		return
	}

	r.actionCycle[update.ActionID] = r.state.Foreground.CycleID

	if r.state.Foreground.State != ForegroundRequiresAction {
		r.blockedCycle = r.state.Foreground.CycleID
	}
}

// patchAction applies a later update to an action the stream already introduced.
// A terminal action is judged first and separately, on the same member-wise
// basis an activity is: it admits nothing but a no-op restatement, which is
// suppressed rather than applied.
func (r *Reducer) patchAction(delivery Delivery, update ActionUpdate) error {
	index := r.actionIndex(update.ActionID)

	existing := r.state.Actions[index]
	if existing.State.Terminal() {
		if detail := terminalActionDifference(existing, update); detail != "" {
			return r.fail(delivery, ViolationPostTerminalMutation, detail)
		}

		return nil
	}

	if detail := immutableActionConflict(existing, update); detail != "" {
		return r.fail(delivery, ViolationImmutableIdentityChange, detail)
	}

	r.state.Actions[index].State = update.State

	return nil
}

// terminalActionDifference reports the first member a restatement carries that
// differs from the reduced terminal record, or the empty string when it carries
// none.
func terminalActionDifference(existing ActionRecord, update ActionUpdate) string {
	if detail := immutableActionConflict(existing, update); detail != "" {
		return detail
	}

	if update.State != existing.State {
		return "action " + update.ActionID + " is terminal"
	}

	return ""
}

func immutableActionConflict(existing ActionRecord, update ActionUpdate) string {
	switch {
	case update.Kind != "" && update.Kind != existing.Kind:
		return "action " + update.ActionID + " changed kind"
	case update.Owner.ID != "" && update.Owner != existing.Owner:
		return "action " + update.ActionID + " changed owner"
	case update.RunID != "" && update.RunID != existing.RunID:
		return "action " + update.ActionID + " changed ownership root"
	case update.BlocksForeground != nil && *update.BlocksForeground != existing.BlocksForeground:
		return "action " + update.ActionID + " changed what it blocks"
	default:
		return ""
	}
}

func (r *Reducer) turnKnown(turnID string) bool {
	_, known := r.turnSeen[turnID]

	return known
}

func (r *Reducer) activityKnown(activityID string) bool {
	_, known := r.activitySeen[activityID]

	return known
}

func (r *Reducer) turnIndex(turnID string) int {
	return indexOf(r.state.Turns, turnID, TurnRecord.identity)
}

func (r *Reducer) activityIndex(activityID string) int {
	return indexOf(r.state.Activities, activityID, ActivityRecord.identity)
}

func (r *Reducer) actionIndex(actionID string) int {
	return indexOf(r.state.Actions, actionID, ActionRecord.identity)
}
