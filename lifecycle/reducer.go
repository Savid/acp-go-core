package lifecycle

import (
	"encoding/json"
	"errors"
)

// Options configures a reducer.
type Options struct {
	// Negotiated are the lifecycle facts the source's configuration proved. The
	// reducer refuses anything the configuration did not advertise.
	Negotiated Negotiated
}

// Reducer reduces one session's lifecycle stream. It validates ordering identity
// before reducing, refuses anything it cannot prove, and latches on the first
// refusal: a stream that failed closed never reduces again.
//
// One reducer follows one session across incarnations. A snapshot bearing a new
// stream identity supersedes the previous incarnation and starts a fresh
// projection; every other event on a foreign or fenced stream is stale.
//
// The one advertised fact it cannot check is updatesOutsidePrompt: no fact on
// the ordered stream expresses whether a prompt is in flight, so that obligation
// belongs to the emitter's own configuration and to a transport-facing consumer.
//
// A Reducer is not safe for concurrent use; the owner of the stream serializes
// reduction.
type Reducer struct {
	negotiated Negotiated
	state      State
	base       uint64
	started    bool
	failed     *ViolationError
	// frames holds every decoded notification this incarnation reduced. Wholesale
	// idempotence has no window: an exact retransmission is suppressed however far
	// back its identity was reduced, and the retention ends with the incarnation.
	frames map[uint64]any
	// turnSeen and activitySeen record which identities the stream introduced.
	turnSeen     map[string]struct{}
	activitySeen map[string]struct{}
	// blockedCycle is the cycle owing the accompanying foreground transition a
	// blocking action requires.
	blockedCycle string
	// actionCycle records, per blocking action, the foreground cycle it stopped: a
	// blocker blocks the cycle current at its first sight, and that cycle may not
	// move again until the blocker terminalizes.
	actionCycle map[string]string
	// retired names every stream identity a later incarnation superseded. It spans
	// incarnations rather than being reset with the projection, because a
	// superseded incarnation is fenced for the rest of the session.
	retired map[string]struct{}
}

// NewReducer builds a reducer for one session.
func NewReducer(opts Options) *Reducer {
	reducer := &Reducer{negotiated: opts.Negotiated, retired: make(map[string]struct{})}
	reducer.reset("")

	return reducer
}

// Negotiated reports the configuration the reducer validates against.
func (r *Reducer) Negotiated() Negotiated { return r.negotiated }

// State returns the projection proved so far.
func (r *Reducer) State() State { return r.state.clone() }

// Failed reports the latched refusal, if any.
func (r *Reducer) Failed() *ViolationError { return r.failed }

// Close records that the addressed session's close completed. The session is
// over: every later event bearing its identity is stale, a would-be new
// incarnation of it included.
func (r *Reducer) Close() { r.state.Closed = true }

// ReduceSessionUpdate decodes one session/update notification payload and reduces
// the delivery it carries. A frame carrying no envelope reports ErrNoEnvelope and
// changes nothing; every other refusal latches the stream, so a caller that stops
// here holds the projection as it stood at the moment of refusal.
func (r *Reducer) ReduceSessionUpdate(params json.RawMessage) error {
	if r.failed != nil {
		return r.failed
	}

	delivery, err := DecodeSessionUpdate(params, r.negotiated)
	if err != nil {
		var refusal *ViolationError
		if errors.As(err, &refusal) {
			r.failed = refusal
			r.nameStream(refusal.StreamID)
		}

		return err
	}

	return r.Reduce(delivery)
}

// nameStream adopts the identity a refused frame named. A stream is identified by
// the envelope naming it, whether or not that envelope reduces, so a refusal
// before the stream ever opened still reports which stream failed closed. An
// already-open stream keeps its own identity.
func (r *Reducer) nameStream(streamID string) {
	if !r.started {
		r.state.StreamID = streamID
	}
}

// Reduce validates and reduces one delivery. Carrier legality is structural, so it
// is judged before ordering. An exact retransmission of an already-reduced
// identity is suppressed wholesale and returns nil without changing the
// projection.
func (r *Reducer) Reduce(delivery Delivery) error {
	switch {
	case r.failed != nil:
		return r.failed
	case delivery.Carrier != CarrierSessionInfo:
		return r.fail(delivery, ViolationIllegalCarrier, "carrier "+string(delivery.Carrier))
	case r.state.Closed:
		return r.fail(delivery, ViolationStaleStream, "the session's close completed")
	case !r.started:
		return r.reduceFirst(delivery)
	case delivery.StreamID != r.state.StreamID:
		return r.reduceForeign(delivery)
	case delivery.Sequence < r.base:
		return r.fail(delivery, ViolationSequenceRegression, "below the stream's snapshot boundary")
	case delivery.Sequence <= r.state.ReducedThrough:
		return r.reduceDuplicate(delivery)
	case delivery.Sequence > r.state.ReducedThrough+1:
		return r.fail(delivery, ViolationSequenceGap, "expected the next contiguous sequence")
	case delivery.Event.Type == EventSnapshot:
		return r.fail(delivery, ViolationStreamCycle, "a snapshot opens a stream and never appears inside one")
	}

	if err := r.apply(delivery); err != nil {
		return err
	}

	r.commit(delivery)

	return nil
}

// reduceForeign admits the next incarnation. Only its opening snapshot may arrive
// on a stream identity this reducer has not seen; a projection is per incarnation
// and adopts nothing from the one it supersedes. Supersession fences the
// incarnation it replaced exactly as close does.
//
// The replacement is judged whole on a projection of its own and swapped in only
// once it proves out, so a snapshot that fails its own validation supersedes
// nothing: the standing projection stays exactly as it stood at the moment of
// refusal.
func (r *Reducer) reduceForeign(delivery Delivery) error {
	if delivery.Event.Type != EventSnapshot {
		return r.fail(delivery, ViolationStaleStream, "stream is "+r.state.StreamID)
	}

	if _, superseded := r.retired[delivery.StreamID]; superseded {
		return r.fail(delivery, ViolationStaleStream, "the incarnation was superseded")
	}

	next := &Reducer{negotiated: r.negotiated, retired: r.retired}
	next.reset(delivery.StreamID)

	if err := next.reduceFirst(delivery); err != nil {
		r.failed = next.failed

		return err
	}

	next.state.Closed = r.state.Closed
	next.retired[r.state.StreamID] = struct{}{}
	*r = *next

	return nil
}

func (r *Reducer) reset(streamID string) {
	r.state = State{StreamID: streamID}
	r.base = 0
	r.started = false
	r.frames = make(map[uint64]any)
	r.turnSeen = make(map[string]struct{})
	r.activitySeen = make(map[string]struct{})
	r.blockedCycle = ""
	r.actionCycle = make(map[string]string)
}

func (r *Reducer) reduceFirst(delivery Delivery) error {
	r.state.StreamID = delivery.StreamID

	if delivery.Event.Type != EventSnapshot {
		return r.fail(delivery, ViolationDeltaBeforeSnapshot, "first event was "+string(delivery.Event.Type))
	}

	r.started = true
	r.base = delivery.Sequence

	if err := r.applySnapshot(delivery); err != nil {
		r.started = false

		return err
	}

	r.commit(delivery)

	return nil
}

func (r *Reducer) reduceDuplicate(delivery Delivery) error {
	if recorded, known := r.frames[delivery.Sequence]; known && valueEqual(recorded, delivery.Frame) {
		r.state.SuppressedRetransmissions++

		return nil
	}

	return r.fail(delivery, ViolationConflictingDuplicate, "the identity already delivered different content")
}

func (r *Reducer) commit(delivery Delivery) {
	r.state.ReducedThrough = delivery.Sequence
	r.frames[delivery.Sequence] = delivery.Frame
}

func (r *Reducer) fail(delivery Delivery, kind ViolationKind, detail string) error {
	r.failed = violation(kind, delivery.StreamID, delivery.Sequence, detail)

	return r.failed
}

func (r *Reducer) apply(delivery Delivery) error {
	switch delivery.Event.Type {
	case EventPromptAccepted:
		return r.applyPromptAccepted(delivery)
	case EventStateUpdate:
		return r.applyStateUpdate(delivery)
	case EventActivityUpdate:
		return r.applyActivityUpdate(delivery)
	default:
		return r.applyActionUpdate(delivery)
	}
}
