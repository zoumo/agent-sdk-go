package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	agenttestify "github.com/zoumo/agent-sdk-testify"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func syntheticScenario(frames []Frame) ([]byte, error) {
	var rows [][]byte
	for _, frame := range frames {
		metadata := map[string]any{"ordinal": frame.Ordinal, "direction": frame.Direction, "channel": frame.Channel, "epoch": frame.Epoch, "elapsedMs": frame.ElapsedMS, "delayMs": frame.DelayMS, "disconnect": frame.Disconnect}
		row, err := json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
		row = append(row[:len(row)-1], []byte(`,"wire":`)...)
		wire := frame.Wire
		if len(wire) == 0 {
			wire = []byte("null")
		}
		row = append(row, wire...)
		rows = append(rows, append(row, '}'))
	}
	data := append([]byte(`{"version":1,"provenance":"synthetic","frames":[`), bytes.Join(rows, []byte(","))...)
	data = append(data, ']', '}')
	return data, nil
}

// mockPeer retains the byte-oriented peer API for external synthetic consumers.
// Native SDK replay tests connect to NewRun directly. The library owns these
// endpoints without the bridge's legacy byte-copy adapter.
func mockPeer(ctx context.Context, in io.Reader, out io.Writer, frames []Frame, handshakes string) error {
	reader, ok := in.(io.ReadCloser)
	if !ok {
		switch in.(type) {
		case *bytes.Buffer, *bytes.Reader, *strings.Reader:
			reader = io.NopCloser(in)
		default:
			return errors.New("synthetic peer requires a closeable input or an in-memory reader")
		}
	}
	writer, ok := out.(io.WriteCloser)
	if !ok {
		switch out.(type) {
		case *bytes.Buffer, *strings.Builder:
			writer = peerWriter{out}
		default:
			if out != io.Discard {
				return errors.New("synthetic peer requires a closeable output or an in-memory writer")
			}
			writer = peerWriter{out}
		}
	}
	data, err := syntheticScenario(frames)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "codex-synthetic-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	session, err := mocktest.Prepare(dir, "codex", data, mocktest.Options{Handshakes: handshakes})
	if err != nil {
		return err
	}
	defer session.Close()
	fixture, err := agenttestify.Open(session.Dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	run, err := fixture.StartMockAgent(ctx, agenttestify.MockAgentOptions{}, agenttestify.StartMockAgentStreams{Reader: reader, Writer: writer, Stderr: io.Discard})
	if err != nil {
		return err
	}
	result, err := run.Wait(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "scripted disconnect") {
			err = errors.Join(mocktest.ErrDisconnected, err)
		}
		return err
	}
	if result == nil || result.Execution == nil {
		return errors.New("execution report missing")
	}
	if result.Execution.Status != agenttestify.ExecutionStatusPassed {
		return fmt.Errorf("execution status %s", result.Execution.Status)
	}
	return session.Close()
}

type peerWriter struct{ io.Writer }

func (peerWriter) Close() error { return nil }
