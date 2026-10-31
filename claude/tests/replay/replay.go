// Package transcript provides Go SDK fixture adapters to the Go mock.
package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type Frame struct {
	Ordinal     int             `json:"ordinal"`
	Direction   string          `json:"direction"`
	Channel     string          `json:"channel"`
	Data        json.RawMessage `json:"data,omitempty"`
	DelayMillis int             `json:"delay_ms,omitempty"`
}

// Replay wraps synthetic JSONL for the protocol process. complete reports frame
// consumption so callers can end input; success also requires a valid client EOF.
func Replay(input io.Reader, output io.Writer, capture io.Reader, complete func() error) error {
	ctx := context.Background()
	data, err := io.ReadAll(io.LimitReader(capture, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("capture byte limit exceeded")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var rows [][]byte
	for scanner.Scan() {
		rows = append(rows, append([]byte(nil), scanner.Bytes()...))
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	wrapped := append([]byte(`{"version":1,"provenance":"synthetic","frames":[`), bytes.Join(rows, []byte(","))...)
	wrapped = append(wrapped, ']', '}')
	return mocktest.Run(ctx, "claude", input, output, wrapped, mocktest.Options{Complete: complete})
}
