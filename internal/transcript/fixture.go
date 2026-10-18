// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const PeerEnvironment = "CLAUDE_GO_RECORDED_PEER"

// This is a real CLI authentication failure, not a successful model recording.
//
//go:embed testdata/real-sanitized/s1-auth-blocked/wire.jsonl
var Wire []byte

//go:embed testdata/real-sanitized/s1-auth-blocked/manifest.json
var Manifest []byte

func Prompt() (string, error) {
	frames := bytes.Split(bytes.TrimSpace(Wire), []byte{'\n'})
	for _, line := range frames {
		var f Frame
		if err := json.Unmarshal(line, &f); err != nil {
			return "", err
		}
		var d struct {
			Type    string `json:"type"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if f.Direction == "client" && json.Unmarshal(f.Data, &d) == nil && d.Type == "user" {
			return d.Message.Content, nil
		}
	}
	return "", fmt.Errorf("fixture has no input")
}
func Peer(input io.Reader, output io.Writer) error {
	return Replay(input, output, bytes.NewReader(Wire), func() error {
		if path := os.Getenv("CLAUDE_GO_REPLAY_COMPLETE"); path != "" {
			return os.WriteFile(path, []byte("complete\n"), 0600)
		}
		return nil
	})
}
