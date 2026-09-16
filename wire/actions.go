package wire

import (
	"context"
	"encoding/json"
)

type actionWriteKey struct {
	streamID string
	actionID string
}

func actionKey(meta map[string]any) actionWriteKey {
	correlation, _ := meta[LifecycleKey].(map[string]any)
	action, _ := correlation["action"].(map[string]any)
	streamID, _ := correlation["streamId"].(string)
	actionID, _ := action["actionId"].(string)

	return actionWriteKey{streamID: streamID, actionID: actionID}
}

func (t *Transport) awaitRequestWrite(meta map[string]any) (<-chan struct{}, func()) {
	key := actionKey(meta)

	t.mu.Lock()
	ch := make(chan struct{})
	t.written[key] = ch
	t.mu.Unlock()

	return ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		if t.written[key] == ch {
			delete(t.written, key)
		}
	}
}

func (t *Transport) releaseRequestWrite(params json.RawMessage) {
	var envelope struct {
		Meta map[string]any `json:"_meta"` //nolint:tagliatelle // ACP reserves this wire spelling.
	}
	if json.Unmarshal(params, &envelope) != nil {
		return
	}

	key := actionKey(envelope.Meta)
	if key.streamID == "" || key.actionID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if ch := t.written[key]; ch != nil {
		delete(t.written, key)
		close(ch)
	}
}

// CallAndAnnounce sends an action-correlated request and announces it only
// after its complete frame is written. Incarnation ids MUST be unique on the
// connection. A request that fails before writing has no announcement. With
// no transport, an embedded client owns request registration and ordering.
func CallAndAnnounce[T any](ctx context.Context, transport *Transport, meta map[string]any, send func(context.Context, map[string]any) (T, error), announce func()) (T, error) {
	type answer struct {
		value T
		err   error
	}

	answers := make(chan answer, 1)

	var written <-chan struct{}

	if transport != nil {
		var release func()

		written, release = transport.awaitRequestWrite(meta)
		defer release()

		var cancel context.CancelFunc

		ctx, cancel = context.WithCancel(ctx)
		defer cancel()

		stop := context.AfterFunc(transport.ctx, cancel)
		defer stop()
	}

	go func() {
		value, err := send(ctx, meta)
		answers <- answer{value: value, err: err}
	}()

	if written != nil {
		select {
		case <-written:
		case result := <-answers:
			select {
			case <-written:
				announce()
			default:
			}

			return result.value, result.err
		}
	}

	announce()

	result := <-answers

	return result.value, result.err
}
