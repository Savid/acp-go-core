package wire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/savid/acp-go-core/process"
)

// TransportFailure waits briefly for a process exit, then reports its status
// and stderr tail or the native transport's error. streamErr is read after the
// wait so a read loop can finish recording its cause.
func TransportFailure(ctx context.Context, proc *process.Process, name string, err error, streamErr func() error) TurnFailure {
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if result, waitErr := proc.Wait(waitCtx); waitErr == nil {
		message := fmt.Sprintf("%s exited with status %d", name, result.ExitCode)
		if result.Signal != 0 {
			message = fmt.Sprintf("%s was killed by signal %d", name, result.Signal)
		}

		if tail := proc.StderrTail(); tail != "" {
			message += ": " + tail
		}

		return TurnFailure{Cause: CauseProcessExit, Message: message}
	}

	if err == nil && streamErr != nil {
		err = streamErr()
	}

	message := name + " stream closed mid-turn"
	if err != nil && !errors.Is(err, io.EOF) {
		message = err.Error()
	}

	return TurnFailure{Cause: CauseTransport, Message: message}
}
