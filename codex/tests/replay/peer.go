package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	"io"
)

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
