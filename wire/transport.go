package wire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/coder/acp-go-sdk"
)

// Transport wraps the JSON-RPC streams Serve hands to the SDK. It reads every
// inbound frame to keep the raw params the lifecycle strictness needs and the
// request ids the establishing responses answer, and it watches every outbound
// frame so a session's opening publication runs only after its establishing
// response is on the wire and an action announcement only after its request.
type Transport struct {
	input     io.Reader
	output    io.Writer
	started   chan struct{}
	startOnce sync.Once
	ctx       context.Context //nolint:containedctx // Owns the opening publications the transport schedules; Close cancels it.
	stop      context.CancelFunc

	mu         sync.Mutex
	requests   map[string]inboundRequest
	raw        map[string][]rawParams
	hooks      map[string]*openingPublication
	ready      map[acp.SessionId]chan struct{}
	written    map[actionWriteKey]chan struct{}
	requestKey string
	partial    []byte
}

type inboundRequest struct {
	method    string
	sessionID acp.SessionId
}

type openingPublication struct {
	sessionID acp.SessionId
	hook      func(context.Context)
	ready     chan struct{}
	previous  <-chan struct{}
}

// rawParams keeps one recorded request's params with the id its response
// carries, so the record is dropped when that response is written.
type rawParams struct {
	id     string
	params json.RawMessage
}

// NewTransport prepares streams that remain unread until Start.
func NewTransport(input io.Reader, output io.Writer) *Transport {
	ctx, stop := context.WithCancel(context.Background())

	return &Transport{
		input:      input,
		started:    make(chan struct{}),
		output:     output,
		ctx:        ctx,
		stop:       stop,
		requests:   make(map[string]inboundRequest),
		requestKey: rand.Text(),
		raw:        make(map[string][]rawParams),
		hooks:      make(map[string]*openingPublication),
		ready:      make(map[acp.SessionId]chan struct{}),
		written:    make(map[actionWriteKey]chan struct{}),
	}
}

// Start releases inbound reads after the SDK connection and its handler are configured.
func (t *Transport) Start() { t.startOnce.Do(func() { close(t.started) }) }

// Close ends the connection's opening publications and releases every waiter.
func (t *Transport) Close() { t.stop() }

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
	<-r.transport.started

	if len(r.pending) == 0 {
		line, err := r.lines.ReadBytes('\n')
		if len(line) > 0 {
			r.transport.observeInbound(line)
			line = r.transport.bindInbound(line)
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

	id := string(f.ID)
	t.requests[id] = inboundRequest{method: f.Method, sessionID: params.SessionID}

	switch f.Method {
	case acp.AgentMethodInitialize:
		t.raw[acp.AgentMethodInitialize] = append(t.raw[acp.AgentMethodInitialize], rawParams{id: id, params: f.Params})
	case acp.AgentMethodSessionPrompt:
		key := rawKeyPrompt(params.SessionID)
		t.raw[key] = append(t.raw[key], rawParams{id: id, params: f.Params})
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

	return queue[0].params
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

		_ = json.Unmarshal(raw.params, &envelope)

		if sameJSON(envelope.Meta[LifecycleKey], meta[LifecycleKey]) {
			t.raw[key] = append(queue[:index:index], queue[index+1:]...)

			return raw.params
		}
	}

	return nil
}

// dropRaw forgets a recorded request once its response has been written, so a
// prompt that never reached its session does not retain its params.
func (t *Transport) dropRaw(key, id string) {
	queue := t.raw[key]
	for index, raw := range queue {
		if raw.id == id {
			t.raw[key] = append(queue[:index:index], queue[index+1:]...)

			return
		}
	}
}

func sameJSON(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)

	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

// RequestContext removes the transport's private establishing-request marker
// from decoded metadata and binds its response identity to the handler context.
// Establishing handlers MUST call it before inspecting or forwarding metadata.
func (t *Transport) RequestContext(ctx context.Context, meta map[string]any) context.Context {
	id, ok := meta[t.requestKey].(string)
	delete(meta, t.requestKey)

	if !ok {
		return ctx
	}

	return context.WithValue(ctx, t, id)
}

// bindInbound carries the request identity through the SDK's typed dispatch.
// The marker exists only between this reader and RequestContext.
func (t *Transport) bindInbound(line []byte) []byte {
	var f frame
	if json.Unmarshal(line, &f) != nil || len(f.ID) == 0 {
		return line
	}

	switch f.Method {
	case acp.AgentMethodSessionNew, acp.AgentMethodSessionLoad, acp.AgentMethodSessionResume:
	default:
		return line
	}

	var params map[string]json.RawMessage
	if json.Unmarshal(f.Params, &params) != nil || params == nil {
		return line
	}

	var meta map[string]json.RawMessage
	if raw := params["_meta"]; raw != nil && json.Unmarshal(raw, &meta) != nil {
		return line
	}

	if meta == nil {
		meta = make(map[string]json.RawMessage)
	}

	meta[t.requestKey], _ = json.Marshal(string(f.ID))
	params["_meta"], _ = json.Marshal(meta)

	var fields map[string]json.RawMessage

	_ = json.Unmarshal(line, &fields)
	fields["params"], _ = json.Marshal(params)
	encoded, _ := json.Marshal(fields)

	return append(encoded, '\n')
}

// RegisterHook schedules a publication after this handler's establishing
// response is written. RequestContext supplies the identity. Successive
// publications for one session wait for their predecessors to finish.
func (t *Transport) RegisterHook(ctx context.Context, sessionID acp.SessionId, hook func(context.Context)) error {
	id, _ := ctx.Value(t).(string)
	t.mu.Lock()
	defer t.mu.Unlock()

	request, ok := t.requests[id]
	if !ok || (request.method != acp.AgentMethodSessionNew && request.method != acp.AgentMethodSessionLoad && request.method != acp.AgentMethodSessionResume) || (request.method != acp.AgentMethodSessionNew && request.sessionID != sessionID) {
		return errors.New("opening publication has no establishing request")
	}

	if pending := t.hooks[id]; pending != nil {
		pending.hook = hook

		return nil
	}

	publication := &openingPublication{sessionID: sessionID, hook: hook, ready: make(chan struct{}), previous: t.ready[sessionID]}
	t.ready[sessionID] = publication.ready
	t.hooks[id] = publication

	return nil
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
		if len(f.ID) != 0 && (f.Method == acp.ClientMethodSessionRequestPermission || f.Method == acp.ClientMethodElicitationCreate) {
			t.releaseRequestWrite(f.Params)
		}

		return
	}

	if len(f.ID) == 0 {
		return
	}

	id := string(f.ID)

	t.mu.Lock()
	request, ok := t.requests[id]
	delete(t.requests, id)

	if !ok {
		t.mu.Unlock()

		return
	}

	switch request.method {
	case acp.AgentMethodInitialize:
		t.dropRaw(acp.AgentMethodInitialize, id)
	case acp.AgentMethodSessionPrompt:
		t.dropRaw(rawKeyPrompt(request.sessionID), id)
	}

	publication := t.hooks[id]
	delete(t.hooks, id)
	t.mu.Unlock()

	if publication != nil {
		go t.publishOpening(publication.sessionID, publication, len(f.Error) == 0)
	}
}

func (t *Transport) publishOpening(sessionID acp.SessionId, publication *openingPublication, success bool) {
	defer func() {
		t.mu.Lock()
		defer t.mu.Unlock()

		if t.ready[sessionID] == publication.ready {
			delete(t.ready, sessionID)
		}

		close(publication.ready)
	}()

	if publication.previous != nil {
		select {
		case <-publication.previous:
		case <-t.ctx.Done():
			return
		}
	}

	if success {
		publication.hook(t.ctx)
	}
}

// AwaitSession waits until the establishing response and opening publications
// have been written, before admitting work that can produce session events.
func (t *Transport) AwaitSession(ctx context.Context, sessionID acp.SessionId) error {
	for {
		t.mu.Lock()
		ready := t.ready[sessionID]
		t.mu.Unlock()

		if ready == nil {
			return nil
		}

		select {
		case <-ready:
		case <-t.ctx.Done():
			return t.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
