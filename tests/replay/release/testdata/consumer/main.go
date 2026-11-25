package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zoumo/agent-sdk-go/replay"
)

func main() {
	if err := checkReplay(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkReplay(tag, sum string) error {
	root, err := os.MkdirTemp(".", "fixture-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	fixture, err := replay.Prepare(replay.PrepareOptions{
		FixtureDir: filepath.Join(root, "fixture"), Agent: "codex", Case: "SDK-CASE-001",
	})
	if err != nil {
		return err
	}
	if fixture.Tool.Version != tag || fixture.Tool.ModuleSum != sum || fixture.Tool.Source != replay.ToolSourceEmbedded {
		return fmt.Errorf("unexpected embedded tool identity: %+v", fixture.Tool)
	}
	material := filepath.Join(root, "material")
	if err := replay.Export(replay.ExportOptions{FixtureDir: fixture.FixtureDir, Output: material}); err != nil {
		return err
	}
	recording, err := os.Open(filepath.Join(material, "raw", "raw.jsonl"))
	if err != nil {
		return err
	}
	defer recording.Close()
	input, err := os.Create(filepath.Join(root, "input"))
	if err != nil {
		return err
	}
	defer input.Close()
	decoder, encoder := json.NewDecoder(recording), json.NewEncoder(input)
	for decoder.More() {
		var frame struct {
			Direction string          `json:"direction"`
			Wire      json.RawMessage `json:"wire"`
		}
		if err := decoder.Decode(&frame); err != nil {
			return err
		}
		if frame.Direction == "c2s" {
			if err := encoder.Encode(frame.Wire); err != nil {
				return err
			}
		}
	}
	if _, err := input.Seek(0, 0); err != nil {
		return err
	}
	output, err := os.Create(filepath.Join(root, "output"))
	if err != nil {
		return err
	}
	defer output.Close()
	result, err := fixture.MockAgent(context.Background(), replay.MockAgentOptions{Timeout: 15 * time.Second},
		replay.Streams{In: input, Out: output, Err: os.Stderr})
	if err != nil {
		return err
	}
	execution := result.Execution
	if execution == nil || !execution.Complete || execution.Status != replay.ExecutionStatusPassed || len(execution.Connections) != 1 {
		return fmt.Errorf("incomplete execution: %+v", execution)
	}
	connection := execution.Connections[0]
	if !connection.FullConsumption || !connection.ClientEOF || !connection.CleanupComplete || connection.NativeLaunched {
		return fmt.Errorf("unexpected replay receipt: %+v", connection)
	}
	return nil
}
