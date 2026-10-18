// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	claude "github.com/zoumo/agent-sdk-go/claude"
)

func TestMain(m *testing.M) {
	if os.Getenv(peerEnvironment) == "1" {
		if err := peer(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOfflineExample(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	expected := strings.Split(strings.TrimSpace(frames), "\n")
	associated := make(map[string]bool)
	created := make(map[string]bool)
	nativeCount, rootResult, childAfter, nested, terminal, closed := 0, false, false, false, false, false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var header struct {
			Type string `json:"type"`
			claude.SubagentInfo
		}
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			t.Fatal(err)
		}
		switch header.Type {
		case "subagent_associated":
			associated[header.Source.AssociationID] = true
			if header.Source.TaskID == "grandchild" && header.Parent != nil && header.Parent.TaskID == "child" {
				nested = true
			}
		case "subagent_created":
			if !associated[header.Source.AssociationID] || created[header.Source.TaskID] {
				t.Fatal(header)
			}
			created[header.Source.TaskID] = true
		case "observation_state":
			closed = closed || header.State == "closed"
		default:
			if nativeCount >= len(expected) || line != expected[nativeCount] {
				t.Fatalf("native changed: %s", line)
			}
			nativeCount++
			e, err := claude.ParseEvent([]byte(line))
			if err != nil {
				t.Fatal(err)
			}
			if e.Result != nil {
				rootResult = true
			}
			if e.Assistant != nil && e.ParentToolUseID != nil && rootResult {
				childAfter = true
			}
			if e.Subtype == "task_updated" && e.System != nil && strings.Contains(string(e.System.Patch), "killed") {
				terminal = true
			}
		}
	}
	if nativeCount != len(expected) || !rootResult || !childAfter || !nested || !terminal || !closed || len(created) != 2 {
		t.Fatalf("native=%d root=%t after=%t nested=%t terminal=%t closed=%t created=%v", nativeCount, rootResult, childAfter, nested, terminal, closed, created)
	}
}
