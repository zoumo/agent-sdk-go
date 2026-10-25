// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"encoding/json"
	"fmt"
	"reflect"

	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

// RootWireGuard checks the executable S1 boundary, independently of Drive. It
// only validates bytes; it never manufactures or answers a native request.
// The proxy serializes Check with capture ordering; callers must do likewise.
type RootWireGuard struct {
	project                                           string
	ledger                                            *Ledger
	initializeID                                      string
	initialized, submitted, read, wrote, readAnswered bool
	seen                                              map[string]bool
	pending                                           map[string]map[string]any
}

func NewRootWireGuard(project string, ledger *Ledger) *RootWireGuard {
	return &RootWireGuard{project: project, ledger: ledger, pending: map[string]map[string]any{}, seen: map[string]bool{}}
}
func (g *RootWireGuard) Check(direction string, raw []byte) error {
	fail := func() error { return fmt.Errorf("outside exact S1 wire policy") }
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return fail()
	}
	if direction == "server" {
		if m["subtype"] == "hook_started" || m["subtype"] == "hook_response" || m["subtype"] == "task_started" || m["subtype"] == "task_progress" || (m["parent_tool_use_id"] != nil && m["parent_tool_use_id"] != "") {
			return fail()
		}
		switch m["type"] {
		case "control_response":
			response, _ := m["response"].(map[string]any)
			if g.initializeID == "" || g.initialized || response["request_id"] != g.initializeID || response["subtype"] != "success" {
				return fail()
			}
			g.initialized = true
		case "control_request":
			id, _ := m["request_id"].(string)
			request, _ := m["request"].(map[string]any)
			input, _ := request["input"].(map[string]any)
			if !g.submitted || id == "" || (g.seen[id] || id == g.initializeID) || request["subtype"] != "can_use_tool" {
				return fail()
			}
			path, _ := input["file_path"].(string)
			switch request["tool_name"] {
			case "Read":
				if g.read || len(input) != 1 || !transcript.SafeFile(g.project, path, "numbers.txt") {
					return fail()
				}
				g.read = true
			case "Write":
				if !g.readAnswered || g.wrote || len(input) != 2 || input["content"] != "total=18\n" || !transcript.SafeFile(g.project, path, "summary.txt") {
					return fail()
				}
				g.wrote = true
			default:
				return fail()
			}
			g.pending[id] = input
			g.seen[id] = true
		case "control_cancel_request":
			return fail()
		}
		return nil
	}
	if direction != "client" {
		return fail()
	}
	switch m["type"] {
	case "control_request":
		id, _ := m["request_id"].(string)
		expected := map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "initialize", "hooks": nil, "forwardSubagentText": true}}
		if id == "" || g.initializeID != "" || !reflect.DeepEqual(m, expected) {
			return fail()
		}
		g.initializeID = id
	case "user":
		expected := map[string]any{"type": "user", "session_id": "", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": s1}}
		if !g.initialized || g.submitted || !reflect.DeepEqual(m, expected) || g.ledger == nil {
			return fail()
		}
		if err := g.ledger.Dispatch("s1"); err != nil {
			return err
		}
		g.submitted = true
	case "control_response":
		response, _ := m["response"].(map[string]any)
		id, _ := response["request_id"].(string)
		input := g.pending[id]
		expected := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": map[string]any{"behavior": "allow", "updatedInput": input}}}
		if input == nil || !reflect.DeepEqual(m, expected) {
			return fail()
		}
		if len(input) == 1 {
			g.readAnswered = true
		}
		delete(g.pending, id)
	default:
		return fail()
	}
	return nil
}
