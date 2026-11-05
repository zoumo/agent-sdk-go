package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
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
	dir, err := os.MkdirTemp("", "codex-synthetic-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	session, err := mocktest.Prepare(dir, "codex", data, mocktest.Options{Handshakes: handshakes})
	if err != nil {
		return err
	}
	runErr := session.Run(ctx, in, out)
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
