package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// stderrTailBytes bounds the stderr the process retains for diagnostics.
const stderrTailBytes = 8 * 1024

// stderrFlushWait bounds how long a reader waits for the stderr copier to
// deliver the child's final line after the child has exited.
const stderrFlushWait = 250 * time.Millisecond

// Request describes one harness launch.
type Request struct {
	Executable string
	Args       []string
	Env        []string
	Dir        string
}

// Result describes a terminal process.
type Result struct {
	ExitCode int
	Signal   int
}

// Process is a running harness with its stdin and stdout pipes and a bounded
// stderr tail it drains itself. Wait may be called from many goroutines; the
// first call begins reaping and every caller sees the same result.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	tailMu     sync.Mutex
	tail       []byte
	tailClosed chan struct{}

	waitOnce sync.Once
	done     chan struct{}
	mu       sync.Mutex
	result   Result
	waitErr  error
}

// Start launches the request as a child in its own process group with three
// dedicated pipes. exec.Cmd's own pipe helpers hand their parent ends to Wait,
// which closes them the moment the child exits and races whoever is still
// draining; owning both ends here keeps each parent end open until its reader
// sees EOF.
func Start(ctx context.Context, request Request) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.Command(request.Executable, request.Args...) //nolint:gosec // The executable is the resolved harness the sibling was configured with.
	cmd.Env = append([]string(nil), request.Env...)
	cmd.Dir = request.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	pipes, err := newPipes(cmd)
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		pipes.closeAll()

		return nil, fmt.Errorf("start %s: %w", request.Executable, err)
	}

	pipes.closeChildEnds()

	p := &Process{
		cmd:        cmd,
		stdin:      pipes.stdin,
		stdout:     pipes.stdout,
		stderr:     pipes.stderr,
		tailClosed: make(chan struct{}),
		done:       make(chan struct{}),
	}

	go p.drainStderr()

	return p, nil
}

// Stdin is the child's standard input.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// Stdout is the child's standard output.
func (p *Process) Stdout() io.ReadCloser { return p.stdout }

// drainStderr copies the child's stderr into the bounded tail until the pipe
// ends.
func (p *Process) drainStderr() {
	defer close(p.tailClosed)

	buffer := make([]byte, 4096)

	for {
		n, err := p.stderr.Read(buffer)
		if n > 0 {
			p.tailMu.Lock()

			p.tail = append(p.tail, buffer[:n]...)
			if len(p.tail) > stderrTailBytes {
				p.tail = p.tail[len(p.tail)-stderrTailBytes:]
			}
			p.tailMu.Unlock()
		}

		if err != nil {
			return
		}
	}
}

// StderrLastLine is the final non-empty stderr line, which is where a dying
// harness names its reason. After the child has exited it waits briefly for
// the copier to deliver the last bytes.
func (p *Process) StderrLastLine() string {
	select {
	case <-p.done:
		select {
		case <-p.tailClosed:
		case <-time.After(stderrFlushWait):
		}
	default:
	}

	p.tailMu.Lock()
	defer p.tailMu.Unlock()

	lines := bytes.Split(bytes.TrimSpace(p.tail), []byte("\n"))
	for index := len(lines) - 1; index >= 0; index-- {
		if line := bytes.TrimSpace(lines[index]); len(line) > 0 {
			return string(line)
		}
	}

	return ""
}

func (p *Process) beginWait() {
	p.waitOnce.Do(func() {
		go func() {
			err := p.cmd.Wait()

			result := Result{}
			if p.cmd.ProcessState != nil {
				result.ExitCode = p.cmd.ProcessState.ExitCode()
				if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					result.Signal = int(status.Signal())
				}

				err = nil
			}

			p.mu.Lock()
			p.result = result
			p.waitErr = err
			p.mu.Unlock()
			close(p.done)
		}()
	})
}

// Wait blocks until the root process exits. Cancelling ctx detaches this caller
// and does not affect the process.
func (p *Process) Wait(ctx context.Context) (Result, error) {
	p.beginWait()

	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()

		return p.result, p.waitErr
	}
}

// Done is closed once the root process has been reaped.
func (p *Process) Done() <-chan struct{} {
	p.beginWait()

	return p.done
}

// Shutdown signals the process group with SIGTERM, waits up to grace, then
// sends SIGKILL and waits for the root to exit or ctx to end.
func (p *Process) Shutdown(ctx context.Context, grace time.Duration) error {
	p.beginWait()

	select {
	case <-p.done:
		return nil
	default:
	}

	_ = p.signalGroup(syscall.SIGTERM)

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-p.done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}

	_ = p.signalGroup(syscall.SIGKILL)

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Kill sends SIGKILL to the process group and begins reaping the root, so a
// killed child never lingers as a zombie.
func (p *Process) Kill() error {
	p.beginWait()

	return p.signalGroup(syscall.SIGKILL)
}

func (p *Process) signalGroup(signal syscall.Signal) error {
	if p.cmd.Process == nil {
		return errors.New("process not started")
	}

	if err := syscall.Kill(-p.cmd.Process.Pid, signal); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}

		return fmt.Errorf("signal process group: %w", err)
	}

	return nil
}

// Close closes the parent ends of the three pipes and then joins the stderr
// copier; the close is what returns the copier's blocked read, so it must
// precede the join. A pipe already closed by its reader or writer is not an
// error.
func (p *Process) Close() error {
	var errs []error

	for _, closer := range []io.Closer{p.stdin, p.stdout, p.stderr} {
		if err := closer.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, err)
		}
	}

	<-p.tailClosed

	return errors.Join(errs...)
}

type pipes struct {
	stdin, stdout, stderr                *os.File
	childStdin, childStdout, childStderr *os.File
}

func newPipes(cmd *exec.Cmd) (_ *pipes, err error) {
	set := &pipes{}

	defer func() {
		if err != nil {
			set.closeAll()
		}
	}()

	set.childStdin, set.stdin, err = os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	set.stdout, set.childStdout, err = os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	set.stderr, set.childStderr, err = os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	cmd.Stdin = set.childStdin
	cmd.Stdout = set.childStdout
	cmd.Stderr = set.childStderr

	return set, nil
}

func (p *pipes) closeChildEnds() {
	for _, file := range []*os.File{p.childStdin, p.childStdout, p.childStderr} {
		if file != nil {
			_ = file.Close()
		}
	}
}

func (p *pipes) closeAll() {
	p.closeChildEnds()

	for _, file := range []*os.File{p.stdin, p.stdout, p.stderr} {
		if file != nil {
			_ = file.Close()
		}
	}
}
