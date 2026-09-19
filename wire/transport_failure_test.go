package wire

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/savid/acp-go-core/process"
	"github.com/stretchr/testify/require"
)

func TestTransportFailurePreservesProcessCause(t *testing.T) {
	t.Parallel()
	proc, err := process.Start(t.Context(), process.Request{Executable: "/bin/sh", Args: []string{"-c", "echo native-failure >&2; exit 7"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	_, err = proc.Wait(t.Context())
	require.NoError(t, err)
	failure := TransportFailure(t.Context(), proc, "native process", io.EOF, nil)
	require.Equal(t, CauseProcessExit, failure.Cause)
	require.Contains(t, failure.Message, "status 7")
	require.Contains(t, failure.Message, "native-failure")
}

func TestTransportFailureReadsStreamCause(t *testing.T) {
	t.Parallel()
	proc, err := process.Start(t.Context(), process.Request{Executable: "/bin/sh", Args: []string{"-c", "read line"}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Kill(); _, _ = proc.Wait(context.WithoutCancel(t.Context())); _ = proc.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	failure := TransportFailure(ctx, proc, "native process", nil, func() error { return errors.New("broken frame") })
	require.Equal(t, TurnFailure{Cause: CauseTransport, Message: "broken frame"}, failure)
	failure = TransportFailure(ctx, proc, "native process", io.EOF, nil)
	require.Equal(t, TurnFailure{Cause: CauseTransport, Message: "native process stream closed mid-turn"}, failure)
}

func TestTransportFailureKeepsFullStderrTailOnWire(t *testing.T) {
	t.Parallel()
	tail := strings.Repeat("x", 16*1024-len("FATAL cause")) + "FATAL cause"
	proc, err := process.Start(t.Context(), process.Request{Executable: "/bin/sh", Args: []string{"-c", "printf %s \"$1\" >&2; exit 7", "sh", tail}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Close() })
	_, err = proc.Wait(t.Context())
	require.NoError(t, err)
	failure := TurnFailed("native", TransportFailure(t.Context(), proc, "native process", io.EOF, nil))
	data, ok := failure.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "native process exited with status 7: "+tail, data[FieldMessage])
}
