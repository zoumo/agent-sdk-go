// Package transcript provides Go SDK fixture adapters to the Go mock.
package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type Frame struct {
	Ordinal     int             `json:"ordinal"`
	Direction   string          `json:"direction"`
	Channel     string          `json:"channel"`
	Data        json.RawMessage `json:"data,omitempty"`
	DelayMillis int             `json:"delay_ms,omitempty"`
}

// Prepare decodes SDK-owned synthetic JSONL and prepares an in-process fixture.
// The caller owns the fixture and constructs its SDK using NewRun's Start.
func Prepare(dir string, capture io.Reader) (*mocktest.Session, error) {
	data, err := io.ReadAll(io.LimitReader(capture, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("capture byte limit exceeded")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var rows [][]byte
	for scanner.Scan() {
		rows = append(rows, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	wrapped := append([]byte(`{"version":1,"provenance":"synthetic","frames":[`), bytes.Join(rows, []byte(","))...)
	wrapped = append(wrapped, ']', '}')
	return mocktest.Prepare(dir, "claude", wrapped, mocktest.Options{})
}

// Replay adapts byte-oriented callers that have not yet moved to SDK streams.
// It starts the library run directly; protocol matching remains library-owned.
// Input/output operations and complete must return promptly. A blocking endpoint
// must implement io.Closer with Close unblocking I/O; Replay closes such endpoints
// on cancellation or failure and joins the copy workers before returning.
func Replay(input io.Reader, output io.Writer, capture io.Reader, complete func() error) (result error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp("", "claude-synthetic-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	session, err := Prepare(dir, capture)
	if err != nil {
		return err
	}
	run, err := session.NewRun(ctx, mocktest.RunOptions{})
	if err != nil {
		return errors.Join(err, session.Close())
	}
	defer func() {
		if closeErr := run.CloseSDK(); closeErr != nil {
			result = errors.Join(result, closeErr)
		}
	}()
	if output == nil {
		output = io.Discard
	}
	closeEndpoints := func() {
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
		if closer, ok := output.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	canceled := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		defer close(canceled)
		_ = run.Abort()
		closeEndpoints()
	})
	defer func() {
		if !stopCancellation() {
			<-canceled
		}
	}()
	inputDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(run.Writer, input)
		inputDone <- errors.Join(err, run.Writer.Close())
	}()
	outputDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(output, run.Reader)
		outputDone <- err
		if err != nil {
			_ = run.Abort()
		}
	}()
	completionDone := run.CloseInputWhenComplete(func() error {
		var err error
		if complete != nil {
			err = complete()
		}
		return errors.Join(err, run.Writer.Close())
	})
	_, runErr := run.Wait(ctx)
	if runErr != nil {
		_ = run.CloseSDK()
		closeEndpoints()
	}
	// A successful fixture may have closed its producer while final bytes are
	// still buffered. Drain the consumer before closing its reader.
	outputErr := <-outputDone
	inputErr := <-inputDone
	runErr = errors.Join(runErr, outputErr, inputErr, <-completionDone)
	_ = run.CloseSDK()
	report, reportErr := session.Report()
	closeErr := session.Close()
	if runErr != nil {
		return errors.Join(runErr, reportErr, closeErr)
	}
	if report == nil {
		return errors.Join(reportErr, closeErr, errors.New("session report missing"))
	}
	if report.Status != "passed" {
		return errors.Join(reportErr, closeErr, fmt.Errorf("session status %s", report.Status))
	}
	return errors.Join(reportErr, closeErr)
}
