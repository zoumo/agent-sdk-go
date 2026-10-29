package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	"io"
	"sync"
)

var ErrDisconnected = mocktest.ErrDisconnected

func RunPeer(ctx context.Context, in io.ReadCloser, out io.WriteCloser, frames []Frame) error {
	if ctx == nil || in == nil || out == nil {
		return errors.New("context and owned streams required")
	}
	var once sync.Once
	closeStreams := func() { once.Do(func() { _ = in.Close(); _ = out.Close() }) }
	stop := context.AfterFunc(ctx, closeStreams)
	defer stop()
	defer closeStreams()
	return runPeer(ctx, in, out, frames, false)
}
func RunStoryPeer(ctx context.Context, in io.Reader, out io.Writer, frames []Frame) error {
	return mockPeer(ctx, in, out, frames, "answer")
}
func runPeer(ctx context.Context, in io.Reader, out io.Writer, frames []Frame, assertion bool) error {
	handshakes := ""
	if assertion {
		handshakes = "answer-and-assert"
	}
	return mockPeer(ctx, in, out, frames, handshakes)
}
func mockPeer(ctx context.Context, in io.Reader, out io.Writer, frames []Frame, handshakes string) error {
	var rows [][]byte
	for _, frame := range frames {
		metadata := map[string]any{"ordinal": frame.Ordinal, "direction": frame.Direction, "channel": frame.Channel, "epoch": frame.Epoch, "elapsedMs": frame.ElapsedMS, "delayMs": frame.DelayMS, "disconnect": frame.Disconnect}
		row, err := json.Marshal(metadata)
		if err != nil {
			return err
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
	return mocktest.Run(ctx, "codex", in, out, data, mocktest.Options{Handshakes: handshakes})
}
