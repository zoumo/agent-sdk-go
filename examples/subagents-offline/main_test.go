// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	claude "github.com/zoumo/claude-agent-sdk-go"
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
	decoder := json.NewDecoder(&output)
	scanner := strings.Split(strings.TrimSpace(frames), "\n")
	associated := make(map[string]bool)
	native, rootResult, childAfter, nested, patchTerminal, closed := 0, false, false, false, false, false
	created := make(map[string]bool)
	childBeforeCreation := false
	rootReceipt := false
	for {
		var r struct {
			claude.TreeRecord
			NativeRaw string `json:"native_raw"`
		}
		err := decoder.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if r.Kind == "SubagentCreated" {
			if created[r.Source.TaskID] || !associated[r.Source.AssociationID] || r.Source.RunID != "" || r.RunScope != "legacy-unknown" {
				t.Fatal(r)
			}
			if r.Source.TaskID == "child" && (!rootResult || !childBeforeCreation) {
				t.Fatal("background creation blocked or preceded child output")
			}
			created[r.Source.TaskID] = true
		}
		if r.Kind == "SubagentAssociated" {
			associated[r.Source.AssociationID] = true
		}
		if r.State == "closed" {
			closed = true
		}
		if r.Native == nil {
			continue
		}
		if native >= len(scanner) || r.NativeRaw != scanner[native] {
			t.Fatalf("native frame changed: %s", r.NativeRaw)
		}
		native++
		if r.Native.Subtype == "background_tasks_changed" && r.RunTerminal {
			t.Fatal("snapshot must not end a run")
		}
		if r.Scope == "child" && !associated[r.Source.AssociationID] {
			t.Fatal("native preceded association")
		}
		if r.Native.Result != nil {
			if !rootReceipt {
				t.Fatal("same-turn tool receipt must precede root result")
			}
			rootResult = true
		}
		if r.Native.User != nil && len(r.Native.ToolUseResult) > 0 {
			if rootResult {
				t.Fatal("same-turn tool receipt followed root result")
			}
			rootReceipt = true
		}
		if r.Source.ToolUseID == "spawn-child" && r.Native.Assistant != nil {
			childBeforeCreation = true
		}
		if r.Source.TaskID == "child" && r.Native.Assistant != nil && rootResult {
			childAfter = true
		}
		if r.Source.TaskID == "grandchild" && r.Parent != nil && r.Parent.TaskID == "child" {
			nested = true
		}
		if r.Native.Subtype == "task_updated" && r.Source.TaskID == "child" {
			patchTerminal = rootResult && r.Source.RunID == "" && r.RunTerminal && r.RunStatus == "killed"
		}
	}
	if native != len(scanner) || !rootResult || !childAfter || !nested || !patchTerminal || !closed || len(created) != 2 {
		t.Fatalf("native=%d root=%t after=%t nested=%t terminal=%t closed=%t", native, rootResult, childAfter, nested, patchTerminal, closed)
	}
}
