// SPDX-License-Identifier: Apache-2.0

// This example runs only its own synthetic peer, never an installed Claude CLI.
package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	claude "github.com/zoumo/agent-sdk-go/claude"
)

const peerEnvironment = "CLAUDE_GO_OFFLINE_TREE_PEER"

//go:embed testdata/events.ndjson
var frames string

func main() {
	var err error
	if os.Getenv(peerEnvironment) == "1" {
		err = peer()
	} else {
		err = run(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "claude-subagents-offline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	client, err := claude.NewClient(ctx, claude.Options{
		CLIPath: executable,
		WorkDir: dir,
		// Replacement environment: no account configuration is inherited.
		Env: []string{"HOME=" + dir, peerEnvironment + "=1", "GORACE=atexit_sleep_ms=0"},
	})
	if err != nil {
		return err
	}
	defer client.Abort()
	if err := client.Send(ctx, "emit synthetic children"); err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	for {
		record, err := client.Receive(ctx) // exactly one consumer for the session and its children
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		// Native frames are printed directly, with their exact original bytes.
		// Derived events use their separate concrete JSON codecs.
		if native, ok := record.(claude.Event); ok {
			if _, err := fmt.Fprintln(output, string(native.Raw)); err != nil {
				return err
			}
		} else if err := encoder.Encode(record); err != nil {
			return err
		}
		native, ok := record.(claude.Event)
		if ok && native.System != nil && native.Subtype == "session_state_changed" &&
			native.System.State != nil && *native.System.State == "idle" {
			// A demo stop policy after all scripted frames. Idle is not proof that every
			// task ended. In an application choose your own explicit Close policy.
			// The root result above did not end this receive loop.
			if err := client.Close(); err != nil {
				return err
			}
		}
	}
}

// peer implements only initialize + one scripted user turn. It is not a model,
// a CLI emulator, or evidence of installed CLI behavior.
func peer() error {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var init struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
			Forward bool   `json:"forwardSubagentText"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &init); err != nil {
		return err
	}
	if init.Type != "control_request" || init.Request.Subtype != "initialize" || !init.Request.Forward {
		return fmt.Errorf("expected initialize with forwardSubagentText=true")
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": init.RequestID, "response": map[string]any{},
		},
	}); err != nil {
		return err
	}
	line, err = reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var turn struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &turn); err != nil {
		return err
	}
	if turn.Type != "user" {
		return fmt.Errorf("expected one user turn")
	}
	if _, err := io.WriteString(os.Stdout, frames); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, reader) // remain alive after root result until Close
	return err
}
