package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestTransportOrdersHooksAfterResponse(t *testing.T) {
	t.Parallel()

	input := `{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/w"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"s2","cwd":"/w"}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"_meta":{"acp-go.dev/lifecycle":{"version":1}}}}` + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"s1","_meta":{"acp-go.dev/lifecycle":{"version":1.0}}}}` + "\n"

	var output bytes.Buffer

	tr := NewTransport(bytes.NewBufferString(input), &output)
	tr.Start()
	consumed, err := io.ReadAll(tr.Reader())
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(consumed)), "\n")
	for i, line := range lines {
		var fields map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &fields))
		params, _ := fields["params"].(map[string]any)
		meta, _ := params["_meta"].(map[string]any)
		tr.RequestContext(t.Context(), meta)
		if len(meta) == 0 {
			delete(params, "_meta")
		}
		cleaned, marshalErr := json.Marshal(fields)
		require.NoError(t, marshalErr)
		require.JSONEq(t, strings.Split(strings.TrimSpace(input), "\n")[i], string(cleaned))
	}

	require.JSONEq(t, `{"_meta":{"acp-go.dev/lifecycle":{"version":1}}}`, string(tr.TakeRaw(acp.AgentMethodInitialize)))
	require.Nil(t, tr.TakeRaw(acp.AgentMethodInitialize))

	raw := tr.TakeRawPrompt("s1", map[string]any{LifecycleKey: map[string]any{"version": float64(1)}})
	require.Contains(t, string(raw), `"version":1.0`)
	require.Nil(t, tr.TakeRawPrompt("s1", nil))

	ran := make(chan acp.SessionId, 2)
	registerTransportHook(t, tr, "1", "s1", func(context.Context) { ran <- "s1" })
	registerTransportHook(t, tr, "2", "s2", func(context.Context) { ran <- "s2" })

	Writer := tr.Writer()
	_, err = Writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"s1"}}` + "\n"))
	require.NoError(t, err)
	require.Equal(t, acp.SessionId("s1"), <-ran)

	_, err = Writer.Write([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":1}}` + "\n"))
	require.NoError(t, err)

	select {
	case id := <-ran:
		t.Fatalf("hook %s ran after an error response", id)
	case <-time.After(50 * time.Millisecond):
	}

	require.Contains(t, output.String(), `"sessionId":"s1"`)
}

func TestTransportRetiresHookOnErrorResponse(t *testing.T) {
	t.Parallel()

	input := `{"jsonrpc":"2.0","id":1,"method":"session/load","params":{"sessionId":"s1","cwd":"/w"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s9","prompt":[]}}` + "\n"

	tr := NewTransport(bytes.NewBufferString(input), io.Discard)
	tr.Start()
	_, err := io.ReadAll(tr.Reader())
	require.NoError(t, err)

	registerTransportHook(t, tr, "1", "s1", func(context.Context) { t.Fatal("hook ran after an error response") })

	_, err = tr.Writer().Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":1}}` + "\n"))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, tr.AwaitSession(ctx, "s1"), "a failed establishing response releases waiters")

	require.NotNil(t, tr.TakeRawPrompt("s9", nil), "the prompt's params are held until its response")
	_, err = tr.Writer().Write([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602}}` + "\n"))
	require.NoError(t, err)

	tr.mu.Lock()
	remaining := len(tr.raw[rawKeyPrompt("s9")])
	tr.mu.Unlock()
	require.Zero(t, remaining, "a prompt that never reached its session does not retain its params")
}

func TestTransportDropsRawPromptOnResponse(t *testing.T) {
	t.Parallel()

	input := `{"jsonrpc":"2.0","id":7,"method":"session/prompt","params":{"sessionId":"s1","prompt":[]}}` + "\n"

	tr := NewTransport(bytes.NewBufferString(input), io.Discard)
	tr.Start()
	_, err := io.ReadAll(tr.Reader())
	require.NoError(t, err)

	_, err = tr.Writer().Write([]byte(`{"jsonrpc":"2.0","id":7,"result":{"stopReason":"end_turn"}}` + "\n"))
	require.NoError(t, err)
	require.Nil(t, tr.TakeRawPrompt("s1", nil))
}

func TestTransportCloseReleasesSessionWaiters(t *testing.T) {
	t.Parallel()

	tr := NewTransport(bytes.NewBuffer(nil), io.Discard)
	tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s1"}}`))
	registerTransportHook(t, tr, "1", "s1", func(context.Context) {})
	tr.Close()

	require.ErrorIs(t, tr.AwaitSession(context.Background(), "s1"), context.Canceled)
}

func TestTransportReleasesRequestWrite(t *testing.T) {
	t.Parallel()

	tr := NewTransport(bytes.NewBuffer(nil), io.Discard)
	written, release := tr.awaitRequestWrite(actionMeta("s1:1", "act-1"))
	defer release()

	frame := `{"jsonrpc":"2.0","id":9,"method":"session/request_permission","params":{"_meta":{"acp-go.dev/lifecycle":{"streamId":"s1:1","action":{"actionId":"act-1"}}}}}` + "\n"
	_, err := tr.Writer().Write([]byte(frame[:10]))
	require.NoError(t, err)

	select {
	case <-written:
		t.Fatal("released before the frame completed")
	default:
	}

	_, err = tr.Writer().Write([]byte(frame[10:]))
	require.NoError(t, err)

	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("request write not released")
	}
}

func TestTransportWaitsForConnectionSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		transport := NewTransport(bytes.NewBufferString("request\n"), io.Discard)
		read := make(chan []byte, 1)
		go func() { data, err := io.ReadAll(transport.Reader()); require.NoError(t, err); read <- data }()
		synctest.Wait()
		select {
		case <-read:
			t.Fatal("input read before connection setup")
		default:
		}
		transport.Start()
		transport.Start()
		synctest.Wait()
		require.Equal(t, []byte("request\n"), <-read)
	})
}

func TestTransportReplacingPendingHookKeepsWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := NewTransport(bytes.NewBuffer(nil), io.Discard)
		defer tr.Close()
		tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s1"}}`))
		registerTransportHook(t, tr, "1", "s1", func(context.Context) { t.Error("replaced hook ran") })
		waited := make(chan error, 1)
		go func() { waited <- tr.AwaitSession(t.Context(), "s1") }()
		synctest.Wait()
		ran := false
		registerTransportHook(t, tr, "1", "s1", func(context.Context) { ran = true })
		tr.observeOutboundFrame([]byte(`{"id":1,"result":{}}`))
		synctest.Wait()
		require.True(t, ran)
		select {
		case err := <-waited:
			require.NoError(t, err)
		default:
			t.Fatal("replacing a pending hook stranded its waiter")
		}
	})
}

func TestTransportKeepsOpeningGenerationsOrdered(t *testing.T) {
	for _, response := range []string{`{"id":2,"result":{}}`, `{"id":2,"error":{"code":1}}`} {
		t.Run(response, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				tr := NewTransport(bytes.NewBuffer(nil), io.Discard)
				defer tr.Close()
				tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s1"}}`))
				tr.observeInbound([]byte(`{"id":2,"method":"session/resume","params":{"sessionId":"s1"}}`))
				release := make(chan struct{})
				registerTransportHook(t, tr, "1", "s1", func(context.Context) { <-release })
				tr.observeOutboundFrame([]byte(`{"id":1,"result":{}}`))
				synctest.Wait()
				ran := false
				registerTransportHook(t, tr, "2", "s1", func(context.Context) { ran = true })
				waited := make(chan error, 1)
				go func() { waited <- tr.AwaitSession(t.Context(), "s1") }()
				tr.observeOutboundFrame([]byte(response))
				synctest.Wait()
				if ran {
					t.Error("a later publication must wait for the running one")
				}
				select {
				case <-waited:
					t.Error("session released before the running publication finished")
				default:
				}
				close(release)
				synctest.Wait()
				require.Equal(t, !strings.Contains(response, "error"), ran)
				select {
				case err := <-waited:
					require.NoError(t, err)
				default:
					t.Fatal("completed publications did not release the session")
				}
			})
		})
	}
}

func TestTransportFinishingHookKeepsLaterBarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := NewTransport(bytes.NewBuffer(nil), io.Discard)
		defer tr.Close()
		tr.observeInbound([]byte(`{"id":1,"method":"session/load","params":{"sessionId":"s1"}}`))
		tr.observeInbound([]byte(`{"id":2,"method":"session/resume","params":{"sessionId":"s1"}}`))
		release := make(chan struct{})
		registerTransportHook(t, tr, "1", "s1", func(context.Context) { <-release })
		tr.observeOutboundFrame([]byte(`{"id":1,"result":{}}`))
		synctest.Wait()
		registerTransportHook(t, tr, "2", "s1", func(context.Context) {})
		close(release)
		synctest.Wait()
		waited := make(chan error, 1)
		go func() { waited <- tr.AwaitSession(t.Context(), "s1") }()
		synctest.Wait()
		select {
		case <-waited:
			t.Error("earlier completion discarded the pending response barrier")
		default:
		}
		tr.observeOutboundFrame([]byte(`{"id":2,"result":{}}`))
		synctest.Wait()
		select {
		case err := <-waited:
			require.NoError(t, err)
		default:
			t.Fatal("session remained blocked after both publications")
		}
	})
}

func registerTransportHook(t *testing.T, tr *Transport, id string, sessionID acp.SessionId, hook func(context.Context)) {
	t.Helper()
	ctx := tr.RequestContext(t.Context(), map[string]any{tr.requestKey: id})
	require.NoError(t, tr.RegisterHook(ctx, sessionID, hook))
}

func actionMeta(streamID, actionID string) map[string]any {
	return map[string]any{LifecycleKey: map[string]any{"version": 1, "streamId": streamID, "action": map[string]any{"actionId": actionID}}}
}

func TestTransportRefusesOverlongInboundLine(t *testing.T) {
	t.Parallel()

	transport := NewTransport(strings.NewReader(strings.Repeat("x", maxInboundLine+1)+"\n"), io.Discard)
	transport.Start()
	t.Cleanup(transport.Close)

	n, err := transport.Reader().Read(make([]byte, 1))
	require.Zero(t, n)
	require.ErrorIs(t, err, errInboundLineTooLong)

	transport = NewTransport(strings.NewReader(strings.Repeat("x", maxInboundLine-1)+"\n"), io.Discard)
	transport.Start()
	t.Cleanup(transport.Close)

	line, err := io.ReadAll(transport.Reader())
	require.NoError(t, err)
	require.Len(t, line, maxInboundLine)
}
