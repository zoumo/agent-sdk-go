package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// stdioFrameTransport owns only the child started by NewStdio and the explicit
// pipe endpoints connected to it. Cmd.Wait never closes stdout, so a final
// response already buffered in the owned pipe remains readable.
type stdioFrameTransport struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *os.File
	stderr *os.File
	reader *lineFrameReader

	reaped      chan struct{}
	processExit chan error
	inputOnce   sync.Once
	pipesOnce   sync.Once
	inputErr    error
}

func startStdioFrameTransport(ctx context.Context, options StdioOptions, limits Limits) (*stdioFrameTransport, error) {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), options.Executable, options.Args...)
	cmd.Env = options.Env
	cmd.Dir = options.Dir
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	all := []*os.File{inR, inW, outR, outW, errR, errW}
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = errW
	if err = cmd.Start(); err != nil {
		for _, file := range all {
			_ = file.Close()
		}
		return nil, err
	}
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()

	t := &stdioFrameTransport{
		cmd:         cmd,
		stdin:       inW,
		stdout:      outR,
		stderr:      errR,
		reader:      newLineFrameReader(outR, limits.MaxFrameBytes),
		reaped:      make(chan struct{}),
		processExit: make(chan error, 1),
	}
	go t.wait()
	return t, nil
}

func (t *stdioFrameTransport) wait() {
	waitErr := t.cmd.Wait()
	close(t.reaped)
	t.processExit <- waitErr
	close(t.processExit)
}

func (t *stdioFrameTransport) ReadFrame() ([]byte, error) {
	return t.reader.ReadFrame()
}

func (t *stdioFrameTransport) WriteFrame(ctx context.Context, payload []byte) error {
	return writeLineFrame(ctx, t.stdin, payload)
}

func (t *stdioFrameTransport) StreamInput() streamInput { return nil }

func (t *stdioFrameTransport) StderrReader() io.Reader { return t.stderr }

func (t *stdioFrameTransport) OwnedProcessExit() <-chan error { return t.processExit }

func (t *stdioFrameTransport) Abort() { _ = t.closeInput() }

func (t *stdioFrameTransport) Shutdown(done, readDone <-chan struct{}, timeout time.Duration) transportShutdown {
	<-done
	started := time.Now()
	_ = t.closeInput()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	grace := time.NewTimer(timeout / 2)
	defer grace.Stop()
	reaped := false
	select {
	case <-t.reaped:
		reaped = true
	case <-grace.C:
	}

	var closeErr error
	if !reaped {
		_ = t.cmd.Process.Kill()
		select {
		case <-t.reaped:
			reaped = true
		case <-deadline.C:
			closeErr = ErrShutdownTimeout
		}
	}
	if reaped {
		drain := min(timeout/2, 100*time.Millisecond)
		if remaining := timeout - time.Since(started); drain > remaining {
			drain = max(remaining, 0)
		}
		if drain > 0 {
			timer := time.NewTimer(drain)
			select {
			case <-readDone:
			case <-timer.C:
			}
			timer.Stop()
		}
	}
	t.closePipes()
	return transportShutdown{closeErr: closeErr}
}

func (t *stdioFrameTransport) closeInput() error {
	t.inputOnce.Do(func() {
		t.inputErr = t.stdin.Close()
	})
	return t.inputErr
}

func (t *stdioFrameTransport) closePipes() {
	t.pipesOnce.Do(func() {
		_ = t.stdout.Close()
		_ = t.stderr.Close()
	})
}

func (c *Client) watchOwnedProcessExit(exit <-chan error) {
	waitErr, ok := <-exit
	if !ok {
		return
	}
	drain := min(c.limits.ShutdownTimeout/2, 100*time.Millisecond)
	timer := time.NewTimer(drain)
	defer timer.Stop()
	select {
	case <-c.readDone:
	case <-timer.C:
		c.fail(fmt.Errorf("codex: owned child exited: %w", errors.Join(io.EOF, waitErr)))
	}
}
