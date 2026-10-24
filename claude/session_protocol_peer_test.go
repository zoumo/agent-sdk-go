// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runTreeFixture(reader *bufio.Reader) {
	var transcript *os.File
	if path := os.Getenv("CLAUDE_GO_TRANSCRIPT"); path != "" {
		var err error
		transcript, err = os.Create(path)
		if err != nil {
			os.Exit(12)
		}
		defer transcript.Close()
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if transcript != nil {
			if _, err := transcript.Write(line); err != nil {
				os.Exit(12)
			}
		}
		var w struct {
			Type      string
			RequestID string `json:"request_id"`
			Message   struct{ Content string }
		}
		if json.Unmarshal(line, &w) != nil {
			os.Exit(12)
		}
		switch w.Type {
		case "user":
			if id, ok := strings.CutPrefix(w.Message.Content, "block_write:"); ok {
				_, keepGoing := permissionBlockedWire(reader, transcript, id)
				if !keepGoing {
					return
				}
				continue
			}
			if w.Message.Content == "disconnect" {
				return
			}
			if w.Message.Content == "emit-invalid-utf8" {
				fmt.Fprintln(os.Stdout, "{\"type\":\"system\",\"session_id\":\""+string([]byte{255})+"\"}")
				continue
			}
			if !json.Valid([]byte(w.Message.Content)) {
				os.Exit(13)
			}
			fmt.Fprintln(os.Stdout, w.Message.Content)
		case "control_request":
			sessionReply(w.RequestID, "")
		case "control_response":
			fixtureEmit(map[string]any{"type": "reply_seen", "wire": json.RawMessage(line)})
		}
	}
}

const creationInit = `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258","tools":["Agent","Task","SendMessage"]}`

func creationRequest(tool, name, input, owner string) string {
	source := ""
	if owner != "" {
		source = `,"parent_tool_use_id":"outer"`
		if owner == "other" {
			source = `,"parent_tool_use_id":"unknown"`
		}
	}
	return fmt.Sprintf(`{"type":"assistant","session_id":"s"%s,"message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, source, tool, name, input)
}

const creationInput = `{"description":"inspect local code","prompt":"Inspect the local source"}`

func creationTask(task, tool, background string) string {
	extra := ""
	if background != "" {
		extra += `,"is_backgrounded":` + background
	}

	return fmt.Sprintf(`{"type":"system","subtype":"task_started","session_id":"s","task_id":%q,"task_type":"local_agent","tool_use_id":%q%s}`, task, tool, extra)
}
func creationResult(tool, agent, status string) string {
	output := fmt.Sprintf(`{"status":%q,"agentId":%q,"description":"inspect local source","prompt":"Inspect source","outputFile":"/synthetic/child.output"}`, status, agent)
	if status == "completed" {
		output = fmt.Sprintf(`{"status":%q,"agentId":%q,"content":[{"type":"text","text":"inspection complete"}],"prompt":"Inspect source","totalToolUseCount":0,"totalDurationMs":1,"totalTokens":1,"usage":{"input_tokens":1,"output_tokens":0,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"server_tool_use":null,"service_tier":null,"cache_creation":null}}`, status, agent)
	}
	return fmt.Sprintf(`{"type":"user","session_id":"s","tool_use_result":%s,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"done"}]}}`, output, tool)
}

const earlyTreeFrame = ` {"type":"assistant","session_id":"root","agent_id":"early","parent_tool_use_id":"not-observed","message":{"content":"before initialize ack"},"extra":null} `
