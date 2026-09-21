package wire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

type openingTestAgent struct {
	acp.Agent
	transport  *Transport
	registered chan struct{}
	release    chan struct{}
	opened     chan struct{}
}

func (a *openingTestAgent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	ctx = a.transport.RequestContext(ctx, params.Meta)
	if len(params.Meta) != 1 {
		return acp.LoadSessionResponse{}, errors.New("transport marker leaked into session metadata")
	}
	if params.Meta["accept"] != true {
		return acp.LoadSessionResponse{}, Backpressure("session_restore")
	}
	if err := a.transport.RegisterHook(ctx, params.SessionId, func(context.Context) { close(a.opened) }); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	close(a.registered)
	<-a.release

	return acp.LoadSessionResponse{}, nil
}

func TestOpeningIdentitySurvivesSDKDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		agentInput, clientOutput := io.Pipe()
		clientInput, agentOutput := io.Pipe()
		defer agentInput.Close()
		defer clientOutput.Close()
		defer clientInput.Close()
		defer agentOutput.Close()
		tr := NewTransport(agentInput, agentOutput)
		defer tr.Close()
		agent := &openingTestAgent{transport: tr, registered: make(chan struct{}), release: make(chan struct{}), opened: make(chan struct{})}
		acp.NewAgentSideConnection(agent, tr.Writer(), tr.Reader())
		client := acp.NewClientSideConnection(nil, clientOutput, clientInput)
		tr.Start()
		first := make(chan error, 1)
		go func() {
			_, err := client.LoadSession(t.Context(), LoadSessionRequest("s", "/w", WithSessionMetaValue(map[string]any{"accept": true})))
			first <- err
		}()
		<-agent.registered
		_, err := client.LoadSession(t.Context(), LoadSessionRequest("s", "/w", WithSessionMetaValue(map[string]any{"accept": false})))
		require.Error(t, err)
		synctest.Wait()
		select {
		case <-agent.opened:
			t.Fatal("refused restore released another request's opening")
		default:
		}
		close(agent.release)
		require.NoError(t, <-first)
		<-agent.opened
	})
}

func TestRestoreResponseCannotDiscardAnotherOpening(t *testing.T) {
	for _, response := range []string{`{"id":2,"error":{"code":-32600}}`, `{"id":2,"result":{}}`} {
		t.Run(response, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tr := NewTransport(nil, io.Discard)
				defer tr.Close()
				tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s"}}`))
				tr.observeInbound([]byte(`{"id":2,"method":"session/load","params":{"sessionId":"s"}}`))
				ran := false
				registerTransportHook(t, tr, "1", "s", func(context.Context) { ran = true })
				waited := make(chan error, 1)
				go func() { waited <- tr.AwaitSession(t.Context(), "s") }()
				tr.observeOutboundFrame([]byte(response))
				synctest.Wait()
				require.False(t, ran)
				select {
				case <-waited:
					t.Fatal("unrelated response released the opening barrier")
				default:
				}
				tr.observeOutboundFrame([]byte(`{"id":1,"result":{}}`))
				synctest.Wait()
				require.True(t, ran)
				require.NoError(t, <-waited)
			})
		})
	}
}

func TestOpeningRequestsRemainOrderedBeforeEitherResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := NewTransport(nil, io.Discard)
		defer tr.Close()
		tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s"}}`))
		tr.observeInbound([]byte(`{"id":2,"method":"session/resume","params":{"sessionId":"s"}}`))
		var order []int
		registerTransportHook(t, tr, "1", "s", func(context.Context) { order = append(order, 1) })
		registerTransportHook(t, tr, "2", "s", func(context.Context) { order = append(order, 2) })
		tr.observeOutboundFrame([]byte(`{"id":2,"result":{}}`))
		synctest.Wait()
		require.Empty(t, order)
		tr.observeOutboundFrame([]byte(`{"id":1,"result":{}}`))
		synctest.Wait()
		require.Equal(t, []int{1, 2}, order)
	})
}

func TestActionWriteWaitersAreScopedToIncarnation(t *testing.T) {
	t.Parallel()
	tr := NewTransport(nil, io.Discard)
	defer tr.Close()
	first, releaseFirst := tr.awaitRequestWrite(actionMeta("a:1", "action-1"))
	defer releaseFirst()
	otherSession, releaseOther := tr.awaitRequestWrite(actionMeta("b:1", "action-1"))
	defer releaseOther()
	newIncarnation, releaseNew := tr.awaitRequestWrite(actionMeta("a:2", "action-1"))
	defer releaseNew()
	notification, err := json.Marshal(map[string]any{"method": "session/update", "params": map[string]any{"_meta": actionMeta("a:1", "action-1")}})
	require.NoError(t, err)
	tr.observeOutboundFrame(notification)
	select {
	case <-first:
		t.Fatal("notification released a request waiter")
	default:
	}
	writeActionFrame(t, tr, "a:1")
	select {
	case <-first:
	default:
		t.Fatal("written request was not released")
	}
	for _, pending := range []<-chan struct{}{otherSession, newIncarnation} {
		select {
		case <-pending:
			t.Fatal("another incarnation was released before its request")
		default:
		}
	}
	writeActionFrame(t, tr, "b:1")
	<-otherSession
	writeActionFrame(t, tr, "a:2")
	<-newIncarnation
}

func TestCallAndAnnounceOrdersAndCleansRequests(t *testing.T) {
	for _, mode := range []string{"written", "failed", "cancelled", "disconnected"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tr := NewTransport(nil, io.Discard)
				defer tr.Close()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				release := make(chan struct{})
				announced := false
				done := make(chan error, 1)
				go func() {
					_, err := CallAndAnnounce(ctx, tr, actionMeta("a:1", "action-1"), func(ctx context.Context, _ map[string]any) (string, error) {
						select {
						case <-release:
						case <-ctx.Done():
							return "", ctx.Err()
						}
						if mode == "failed" {
							return "", errors.New("request refused before write")
						}
						writeActionFrame(t, tr, "a:1")

						return "answer", nil
					}, func() { announced = true })
					done <- err
				}()
				synctest.Wait()
				require.False(t, announced)
				switch mode {
				case "cancelled":
					cancel()
				case "disconnected":
					tr.Close()
				default:
					close(release)
				}
				err := <-done
				if mode == "written" {
					require.NoError(t, err)
					require.True(t, announced)
				} else {
					require.Error(t, err)
					require.False(t, announced)
				}
				require.Empty(t, tr.written)
			})
		})
	}
}

func writeActionFrame(t *testing.T, tr *Transport, streamID string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": 9, "method": "elicitation/create", "params": map[string]any{"_meta": actionMeta(streamID, "action-1")}})
	require.NoError(t, err)
	tr.observeOutboundFrame(data)
}
