// Package mocktest bridges Go SDK fixture assertions to the Go mock
// peer. It implements no native matching or request-correlation state.
package mocktest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var ErrDisconnected = errors.New("scripted peer disconnect")

type Options struct {
	Handshakes       string
	MaxFrames        int
	RecordingVersion string
	Complete         func() error
}

// Run uses only the explicitly configured mock executable. It never
// resolves an agent executable, authentication, HOME or provider configuration.
// Blocking non-file streams must implement io.Closer so cancellation and early
// peer exit can interrupt I/O. Run may close those streams during cleanup.
func Run(ctx context.Context, provider string, in io.Reader, out io.Writer, scenario []byte, options Options) error {
	if ctx == nil {
		return errors.New("peer context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	executable, err := Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "sdk-go-peer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "scenario.json")
	if err = os.WriteFile(path, scenario, 0600); err != nil {
		return err
	}
	args := []string{"protocol", "mock", "--provider", provider, "--scenario", path}
	if options.Handshakes != "" {
		args = append(args, "--sdk-test-handshakes", options.Handshakes)
	}
	if options.MaxFrames != 0 {
		args = append(args, "--max-frames", strconv.Itoa(options.MaxFrames))
	}
	if options.RecordingVersion != "" {
		args = append(args, "--recording-version", options.RecordingVersion)
	}
	var progressR, progressW *os.File
	if options.Complete != nil {
		progressR, progressW, err = os.Pipe()
		if err != nil {
			return err
		}
		defer progressR.Close()
		defer progressW.Close()
		args = append(args, "--progress-fd", "3")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	// Own stdout copying too: exec.Cmd.Wait otherwise waits for a blocked
	// caller writer before exposing an already exited peer's error.
	var outputR, outputW *os.File
	if file, ok := out.(*os.File); ok {
		cmd.Stdout = file
	} else {
		outputR, outputW, err = os.Pipe()
		if err != nil {
			return err
		}
		defer outputR.Close()
		defer outputW.Close()
		cmd.Stdout = outputW
	}
	// Own the stdin pump so cmd.Wait can observe process exit before an idle
	// upstream pipe reaches EOF. os/exec's implicit pump otherwise delays exit
	// codes (including timeout/disconnect) until the parent context expires.
	var stdin io.WriteCloser
	if file, ok := in.(*os.File); ok {
		// Pass real descriptors directly. Inherited process stdin can be a
		// blocking descriptor whose Read cannot be interrupted by File.Close.
		cmd.Stdin = file
	} else {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return err
		}
		defer stdin.Close()
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if progressW != nil {
		cmd.ExtraFiles = []*os.File{progressW}
	}
	// Close each stream once, including when process exit and cancellation race.
	var inputClose, outputClose sync.Once
	closeInput := func() {
		inputClose.Do(func() {
			if c, ok := in.(io.Closer); ok {
				_ = c.Close()
			}
		})
	}
	closeOutput := func() {
		outputClose.Do(func() {
			if c, ok := out.(io.Closer); ok {
				_ = c.Close()
			}
		})
	}
	cleanupDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cleanupDone)
		closeInput()
		closeOutput()
	})
	defer func() {
		if !stop() {
			<-cleanupDone
		}
	}()
	if err = cmd.Start(); err != nil {
		return err
	}
	peerExited := make(chan struct{})
	var outputDone chan error
	if outputR != nil {
		_ = outputW.Close()
		outputDone = make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(out, outputR)
			if copyErr != nil {
				select {
				case <-peerExited:
				default:
					cancel()
				}
			}
			outputDone <- copyErr
		}()
	}
	var inputDone chan error
	if stdin != nil {
		inputDone = make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(stdin, in)
			_ = stdin.Close()
			inputDone <- copyErr
		}()
	}
	var callbackErr error
	if progressW != nil {
		_ = progressW.Close()
		var marker [1]byte
		if n, _ := progressR.Read(marker[:]); n == 1 && marker[0] == '1' {
			callbackErr = options.Complete()
			if callbackErr != nil {
				cancel()
			}
		}
	}
	err = cmd.Wait()
	close(peerExited)
	// Process exit does not discard buffered output. Even a failed peer may
	// have emitted final frames. A slow consumer drains them; cancellation
	// closes its stream if backpressure prevents that drain from finishing.
	if inputDone != nil {
		closeInput()
	}
	var outputErr error
	if outputDone != nil {
		outputErr = <-outputDone
	}
	// All callers use finite in-memory readers or closable streams. Closing an
	// owned stream after process exit joins the pump even on early disconnect.
	var inputErr error
	if inputDone != nil {
		closeInput()
		inputErr = <-inputDone
	}
	if callbackErr != nil {
		return callbackErr
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), outputErr)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 4 {
				return ErrDisconnected
			}
			if exit.ExitCode() == 3 {
				return context.DeadlineExceeded
			}
		}
		return fmt.Errorf("Go protocol mock: %w: %s", err, stderr.String())
	}
	if outputErr != nil {
		return fmt.Errorf("Go protocol mock output: %w", outputErr)
	}
	if inputErr != nil {
		return fmt.Errorf("Go protocol mock input: %w", inputErr)
	}
	return nil
}
