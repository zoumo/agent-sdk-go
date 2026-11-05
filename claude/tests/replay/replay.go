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

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type Frame struct {
	Ordinal     int             `json:"ordinal"`
	Direction   string          `json:"direction"`
	Channel     string          `json:"channel"`
	Data        json.RawMessage `json:"data,omitempty"`
	DelayMillis int             `json:"delay_ms,omitempty"`
}

// Replay owns one prepared claude session while adapting synthetic JSONL for a
// protocol process. complete reports frame consumption; the session is removed
// on every path after the protocol process exits.
func Replay(input io.Reader, output io.Writer, capture io.Reader, complete func() error) error {
	ctx := context.Background()
	data, err := io.ReadAll(io.LimitReader(capture, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return errors.New("capture byte limit exceeded")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var rows [][]byte
	for scanner.Scan() {
		rows = append(rows, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	wrapped := append([]byte(`{"version":1,"provenance":"synthetic","frames":[`), bytes.Join(rows, []byte(","))...)
	wrapped = append(wrapped, ']', '}')
	dir, err := os.MkdirTemp("", "claude-synthetic-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	session, err := mocktest.Prepare(dir, "claude", wrapped, mocktest.Options{Complete: complete})
	if err != nil {
		return err
	}
	runErr := session.Run(ctx, input, output)
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
