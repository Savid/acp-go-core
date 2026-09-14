package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// Transport wraps the JSON-RPC streams Serve hands to the SDK. It reads every
// inbound frame to keep the raw params the lifecycle strictness needs and the
// request ids the establishing responses answer, and it watches every outbound
// frame so a session's opening publication runs only after its establishing
// response is on the wire and an action announcement only after its request.
type Transport struct {
	input  io.Reader
	output io.Writer

	mu       sync.Mutex
	requests map[string]inboundRequest
	raw      map[string][]json.RawMessage
	hooks    map[acp.SessionId]func(context.Context)
	ready    map[acp.SessionId]chan struct{}
	written  map[string]chan struct{}
	partial  []byte
}

type inboundRequest struct {
	method    string
	sessionID acp.SessionId
}

// NewTransport wraps the streams supplied to the ACP SDK.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	return &Transport{
		input:    input,
		output:   output,
		requests: make(map[string]inboundRequest),
		raw:      make(map[string][]json.RawMessage),
		hooks:    make(map[acp.SessionId]func(context.Context)),
		ready:    make(map[acp.SessionId]chan struct{}),
		written:  make(map[string]chan struct{}),
	}
}

func (t *Transport) Reader() io.Reader {
	return &transportReader{transport: t, lines: bufio.NewReader(t.input)}
}

func (t *Transport) Writer() io.Writer { return &transportWriter{transport: t} }

type transportReader struct {
	transport *Transport
	lines     *bufio.Reader
	pending   []byte
}

func (r *transportReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		line, err := r.lines.ReadBytes('\n')
		if len(line) > 0 {
			r.transport.observeInbound(line)
		}

		r.pending = line

		if len(line) == 0 {
			return 0, err
		}
	}

	n := copy(p, r.pending)
	r.pending = r.pending[n:]

	return n, nil
}

type transportWriter struct {
	transport *Transport
}

func (w *transportWriter) Write(p []byte) (int, error) {
	n, err := w.transport.output.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}

	if n > 0 {
		w.transport.observeOutbound(p[:n])
	}

	return n, err
}

type frame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type sessionParams struct {
	SessionID acp.SessionId `json:"sessionId"`
}

// observeInbound records the request id and, for the surfaces whose owned
// metadata must be read strictly, the raw params.
func (t *Transport) observeInbound(line []byte) {
	var f frame
	if err := json.Unmarshal(line, &f); err != nil || f.Method == "" || len(f.ID) == 0 {
		return
	}

	var params sessionParams

	_ = json.Unmarshal(f.Params, &params)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.requests[string(f.ID)] = inboundRequest{method: f.Method, sessionID: params.SessionID}

	switch f.Method {
	case acp.AgentMethodInitialize:
		t.raw[acp.AgentMethodInitialize] = append(t.raw[acp.AgentMethodInitialize], f.Params)
	case acp.AgentMethodSessionPrompt:
		t.raw[rawKeyPrompt(params.SessionID)] = append(t.raw[rawKeyPrompt(params.SessionID)], f.Params)
	}
}

func rawKeyPrompt(sessionID acp.SessionId) string {
	return "prompt/" + string(sessionID)
}

// TakeRaw returns the oldest raw params recorded under key.
func (t *Transport) TakeRaw(key string) json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()

	queue := t.raw[key]
	if len(queue) == 0 {
		return nil
	}

	t.raw[key] = queue[1:]

	return queue[0]
}

// TakeRawPrompt returns the recorded raw params of the prompt whose decoded
// lifecycle value equals meta's, so concurrent prompts on one session cannot
// swap correlations.
func (t *Transport) TakeRawPrompt(sessionID acp.SessionId, meta map[string]any) json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := rawKeyPrompt(sessionID)
	queue := t.raw[key]

	for index, raw := range queue {
		var envelope struct {
			Meta map[string]any `json:"_meta"` //nolint:tagliatelle // ACP reserves this wire spelling.
		}

		_ = json.Unmarshal(raw, &envelope)

		if sameJSON(envelope.Meta[LifecycleKey], meta[LifecycleKey]) {
			t.raw[key] = append(queue[:index:index], queue[index+1:]...)

			return raw
		}
	}

	return nil
}

func sameJSON(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)

	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

// RegisterHook schedules one publication to run after the establishing
// response for sessionID is written.
func (t *Transport) RegisterHook(sessionID acp.SessionId, hook func(context.Context)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ready := make(chan struct{})
	t.ready[sessionID] = ready
	t.hooks[sessionID] = func(ctx context.Context) {
		defer close(ready)

		hook(ctx)
	}
}

// AwaitRequestWrite returns a channel closed once the outbound request that
// carries actionID has been written.
func (t *Transport) AwaitRequestWrite(actionID string) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.written[actionID]
	if !ok {
		ch = make(chan struct{})
		t.written[actionID] = ch
	}

	return ch
}

// observeOutbound watches complete frames. A response to a session-establishing
// request releases that session's hook; a request carrying an action
// correlation releases its announcement.
func (t *Transport) observeOutbound(data []byte) {
	t.mu.Lock()
	t.partial = append(t.partial, data...)

	var lines [][]byte

	for {
		index := bytes.IndexByte(t.partial, '\n')
		if index < 0 {
			break
		}

		lines = append(lines, bytes.Clone(t.partial[:index]))
		t.partial = t.partial[index+1:]
	}
	t.mu.Unlock()

	for _, line := range lines {
		t.observeOutboundFrame(line)
	}
}

func (t *Transport) observeOutboundFrame(line []byte) {
	var f frame
	if err := json.Unmarshal(line, &f); err != nil {
		return
	}

	if f.Method != "" {
		t.releaseRequestWrite(f.Params)

		return
	}

	if len(f.ID) == 0 {
		return
	}

	t.mu.Lock()
	request, ok := t.requests[string(f.ID)]
	delete(t.requests, string(f.ID))

	var hook func(context.Context)

	if ok && len(f.Error) == 0 {
		sessionID := request.sessionID

		switch request.method {
		case acp.AgentMethodSessionNew:
			var result sessionParams

			_ = json.Unmarshal(f.Result, &result)
			sessionID = result.SessionID
		case acp.AgentMethodSessionLoad, acp.AgentMethodSessionResume:
		default:
			sessionID = ""
		}

		if sessionID != "" {
			hook = t.hooks[sessionID]
			delete(t.hooks, sessionID)
		}
	}
	t.mu.Unlock()

	if hook != nil {
		go hook(context.Background())
	}
}

func (t *Transport) releaseRequestWrite(params json.RawMessage) {
	var envelope struct {
		Meta map[string]any `json:"_meta"` //nolint:tagliatelle // ACP reserves this wire spelling.
	}

	if err := json.Unmarshal(params, &envelope); err != nil {
		return
	}

	correlation, _ := envelope.Meta[LifecycleKey].(map[string]any)
	action, _ := correlation["action"].(map[string]any)
	actionID, _ := action["actionId"].(string)

	if strings.TrimSpace(actionID) == "" {
		return
	}

	t.mu.Lock()
	ch, ok := t.written[actionID]
	delete(t.written, actionID)
	t.mu.Unlock()

	if ok {
		close(ch)
	}
}

// AwaitSession waits until the establishing response and opening publications
// have been written, before admitting work that can produce session events.
func (t *Transport) AwaitSession(ctx context.Context, sessionID acp.SessionId) error {
	t.mu.Lock()
	ready := t.ready[sessionID]
	t.mu.Unlock()

	if ready == nil {
		return nil
	}

	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
