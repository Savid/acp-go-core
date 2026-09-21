package lifecycle

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Cycle identifies a foreground turn and the native cause that owns it.
type Cycle struct {
	TurnID      string
	CycleID     string
	Origin      Cause
	incarnation string
}

// Publisher owns one session's lifecycle incarnation and blocking actions. It
// serializes its own publications; its zero value is ready. The deliver
// function given to Open runs synchronously under the publisher's lock, on a
// context the caller's cancellation does not reach, and MUST NOT call back
// into the Publisher. A failed delivery fences the stream.
type Publisher struct {
	mu       sync.Mutex
	stream   *stream
	seq      uint64
	blockers map[string]string
	deliver  func(context.Context, map[string]any) error
}

// Active reports whether an unfenced incarnation is open.
func (p *Publisher) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.active()
}

func (p *Publisher) active() bool { return p.stream != nil && !p.stream.fenced() }

// NextID allocates an identity within this session's ordered native events.
func (p *Publisher) NextID(kind string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.nextID(kind)
}

func (p *Publisher) nextID(kind string) string {
	p.seq++

	return fmt.Sprintf("%s-%d", kind, p.seq)
}

// Open publishes an idle snapshot for a new native incarnation.
func (p *Publisher) Open(ctx context.Context, id string, negotiated Negotiated, deliver func(context.Context, map[string]any) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !negotiated.Present() || p.active() {
		return nil
	}

	p.stream = newStream(id, negotiated)
	p.deliver = deliver
	p.blockers = make(map[string]string)

	return p.emit(ctx, snapshotEvent(p.nextID("cycle")))
}

// Accept publishes prompt acceptance and opens its foreground cycle.
func (p *Publisher) Accept(ctx context.Context, c *Cycle, submission Submission) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active() {
		return nil
	}

	c.TurnID = p.nextID("turn")
	c.CycleID = p.nextID("cycle")
	c.Origin = CauseSubmission

	if err := p.emit(ctx, acceptedEvent(submission, c.TurnID)); err != nil {
		return err
	}

	return p.emit(ctx, transitionEvent(ForegroundRunning, c.CycleID, c.TurnID, c.Origin))
}

// NewAgentCycle allocates an immutable identity in the current incarnation.
// The caller reserves its foreground with this identity before publishing it.
func (p *Publisher) NewAgentCycle() Cycle {
	p.mu.Lock()
	defer p.mu.Unlock()

	c := Cycle{Origin: CauseActivity}
	if p.active() {
		c.TurnID = p.nextID("turn")
		c.CycleID = p.nextID("cycle")
		c.incarnation = p.stream.id
	}

	return c
}

// OpenAgentCycle publishes the reserved native activity in its incarnation.
func (p *Publisher) OpenAgentCycle(ctx context.Context, c Cycle) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active() {
		return nil
	}

	if c.incarnation != p.stream.id || c.Origin != CauseActivity || c.TurnID == "" || c.CycleID == "" {
		return fmt.Errorf("agent cycle does not belong to the current incarnation")
	}

	return p.emit(ctx, transitionEvent(ForegroundRunning, c.CycleID, c.TurnID, c.Origin))
}

// ActionPending publishes a blocking action and, for the cycle's first
// blocker, the requires_action transition.
func (p *Publisher) ActionPending(ctx context.Context, c Cycle, actionID string, kind ActionKind) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active() || c.TurnID == "" {
		return nil
	}

	owner := Owner{Type: OwnerTurn, ID: c.TurnID}

	if err := p.emit(ctx, actionEvent(pendingAction(actionID, kind, owner, true))); err != nil {
		return err
	}

	if p.pendingFor(c.CycleID) == 0 {
		if err := p.emit(ctx, transitionEvent(ForegroundRequiresAction, c.CycleID, c.TurnID, c.Origin)); err != nil {
			return err
		}
	}

	p.blockers[actionID] = c.CycleID

	return nil
}

// ActionResolved terminalizes a blocker and resumes running after the cycle's
// last one.
func (p *Publisher) ActionResolved(ctx context.Context, c Cycle, actionID string, state ActionState) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if cycleID, pending := p.blockers[actionID]; !pending || cycleID != c.CycleID || !p.active() {
		return nil
	}

	delete(p.blockers, actionID)

	if err := p.emit(ctx, actionEvent(resolvedAction(actionID, state))); err != nil {
		return err
	}

	if p.pendingFor(c.CycleID) > 0 {
		return nil
	}

	return p.emit(ctx, transitionEvent(ForegroundRunning, c.CycleID, c.TurnID, c.Origin))
}

// Idle cancels the cycle's outstanding blockers and ends a durably committed
// cycle. A failed outcome carries no stop reason.
func (p *Publisher) Idle(ctx context.Context, c Cycle, stopReason string, outcome Outcome) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.active() || c.TurnID == "" {
		return nil
	}

	for _, actionID := range p.blockedBy(c.CycleID) {
		if err := p.emit(ctx, actionEvent(resolvedAction(actionID, ActionCancelled))); err != nil {
			return err
		}

		delete(p.blockers, actionID)
	}

	if outcome == OutcomeFailed {
		stopReason = ""
	}

	return p.emit(ctx, idleEvent(c.CycleID, c.TurnID, c.Origin, stopReason, outcome))
}

// Fence ends the current incarnation without asserting a durable terminal idle.
func (p *Publisher) Fence() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.fence()
}

func (p *Publisher) fence() {
	if p.stream != nil {
		p.stream.fence()
	}
}

// Correlation returns the metadata for an action owned by the given cycle.
func (p *Publisher) Correlation(c Cycle, actionID string) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()

	if actionID == "" || !p.active() {
		return nil
	}

	return map[string]any{MetaKey: actionCorrelation{streamID: p.stream.id, actionID: actionID, owner: Owner{Type: OwnerTurn, ID: c.TurnID}}.value()}
}

func (p *Publisher) pendingFor(cycleID string) int { return len(p.blockedBy(cycleID)) }

// blockedBy lists the cycle's outstanding action ids in a stable order so the
// cancellations Idle emits are the same on every run.
func (p *Publisher) blockedBy(cycleID string) []string {
	var ids []string

	for actionID, owner := range p.blockers {
		if owner == cycleID {
			ids = append(ids, actionID)
		}
	}

	slices.Sort(ids)

	return ids
}

func (p *Publisher) emit(ctx context.Context, event Event) error {
	envelope, err := p.stream.emit(event)
	if err != nil {
		p.fence()

		return fmt.Errorf("lifecycle stream refused an event: %w", err)
	}

	if p.deliver != nil {
		if err := p.deliver(context.WithoutCancel(ctx), envelope); err != nil {
			p.fence()

			return err
		}
	}

	return nil
}
