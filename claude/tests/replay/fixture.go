// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"io"
	"os"

	fixturedata "github.com/zoumo/agent-sdk-go/claude/tests/testdata"
)

const PeerEnvironment = "CLAUDE_GO_SYNTHETIC_PEER"

// TaskFailureWire is independently authored synthetic protocol data.
var TaskFailureWire = fixtureBytes("integration/task-failure.jsonl")

func fixtureBytes(path string) []byte {
	data, err := fixturedata.Files.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

const TaskFailurePrompt = "Exercise a synthetic task failure."

func Peer(input io.Reader, output io.Writer) error {
	return Replay(input, output, bytes.NewReader(TaskFailureWire), func() error {
		if path := os.Getenv("CLAUDE_GO_REPLAY_COMPLETE"); path != "" {
			return os.WriteFile(path, []byte("complete\n"), 0o600)
		}
		return nil
	})
}
