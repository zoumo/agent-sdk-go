package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Synthetic native envelopes, source pins: codex/docs/protocol/README.md and claude/docs/type-sources.md.
// The executable is this test binary, never a real CLI. The trace is peer input,
// not a private SDK queue or graph. Each native protocol keeps its own envelopes.
func sessionTargetPeer(provider, scenario string) error {
	trace, err := os.OpenFile(os.Getenv("SDK_INTEGRATION_TRACE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer trace.Close()
	launch, _ := json.Marshal(map[string]any{"type": "launch", "args": os.Args})
	_, _ = trace.Write(append(launch, '\n'))
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	emit := func(v any) {
		b, _ := json.Marshal(v)
		if provider == "claude" {
			b = completeClaudeFixture(b)
		}
		fmt.Fprintln(os.Stdout, string(b))
	}
	raw := func(s string) {
		if provider == "claude" {
			s = string(completeClaudeFixture(json.RawMessage(s)))
		}
		fmt.Fprintln(os.Stdout, s)
	}
	root := "root"
	if provider == "claude" {
		root = claudeScenarioID
	}
	marker := func(kind string, extra map[string]any) {
		if extra == nil {
			extra = map[string]any{}
		}
		if provider == "codex" {
			extra["threadId"] = root
			emit(map[string]any{"method": "fixture/" + kind, "params": extra})
		} else {
			extra["type"] = "system"
			extra["subtype"] = "status"
			extra["fixture"] = "fixture_" + kind
			extra["session_id"] = root
			emit(extra)
		}
	}
	complete := func(failed bool) {
		if provider == "codex" {
			status := "completed"
			if failed {
				status = "failed"
			}
			raw(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":"turn","status":%q,"items":[],"error":null}}}`, root, status))
		} else {
			emit(map[string]any{"type": "result", "session_id": root, "subtype": "success", "is_error": failed, "result": "done"})
		}
	}
	request := func(id string, question bool) {
		if provider == "codex" {
			method := "item/commandExecution/requestApproval"
			params := `{"threadId":"root","turnId":"turn","itemId":"tool","command":"echo fixture","cwd":"/tmp","reason":null,"availableDecisions":["accept","decline"],"startedAtMs":1,"future":42}`
			if question {
				method = "item/tool/requestUserInput"
				params = `{"threadId":"root","turnId":"turn","itemId":"q","isBlocking":false,"questions":[{"id":"q","header":"Choice","question":"Choose?","isOther":true,"isSecret":false,"options":[{"label":"A","description":"first"}]}],"future":42}`
			}
			raw(fmt.Sprintf(`{"id":%q,"method":%q,"params":%s,"future_envelope":{"n":9007199254740993}}`, id, method, strings.ReplaceAll(params, `"threadId":"root"`, fmt.Sprintf(`"threadId":%q`, root))))
		} else {
			tool, input := "Bash", json.RawMessage(`{"command":"echo fixture","future":42}`)
			if question {
				tool = "AskUserQuestion"
				input = json.RawMessage(`{"questions":[{"question":"Choose?","options":[{"label":"A"}]}],"future":42}`)
			}
			emit(map[string]any{"type": "control_request", "request_id": id, "future_envelope": json.RawMessage(`{"n":9007199254740993}`), "request": map[string]any{"subtype": "can_use_tool", "tool_name": tool, "tool_use_id": "tool", "input": input}})
		}
	}
	starts, inputs, interrupts := 0, 0, 0
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if _, err = trace.Write(append(line, '\n')); err != nil {
			return err
		}
		var f struct {
			ID           json.RawMessage
			Method, Type string
			Params       json.RawMessage
			RequestID    string `json:"request_id"`
			Request      struct{ Subtype string }
			Message      struct{ Content json.RawMessage }
		}
		if err = json.Unmarshal(line, &f); err != nil {
			return err
		}
		reply := func(result string) {
			if provider == "codex" {
				raw(fmt.Sprintf(`{"id":%s,"result":%s}`, f.ID, result))
			} else {
				raw(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":%s}}`, f.RequestID, result))
			}
		}
		switch {
		case f.Method == "initialize":
			reply(`{"userAgent":"synthetic","codexHome":"/synthetic-home","platformFamily":"unix","platformOs":"linux"}`)
		case f.Method == "initialized":
		case f.Method == "thread/start":
			starts++
			id := root
			if scenario == "session-resume" && starts > 1 {
				id = "other"
			}
			reply(fmt.Sprintf(`{"thread":{"id":%q,"cwd":"/synthetic","sessionId":"native-session","projectId":null,"cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"ephemeral":false,"modelProvider":"synthetic","preview":"","source":"appServer","status":{"type":"idle"},"turns":[]},"model":"creation-model","modelProvider":"synthetic","cwd":"/synthetic","approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"readOnly"}}`, id))
		case f.Type == "control_request" && f.Request.Subtype == "initialize":
			reply(`{}`)
			emit(map[string]any{"type": "system", "subtype": "init", "session_id": root, "claude_code_version": "2.1.258", "model": "creation-model"})
		case f.Method == "thread/unsubscribe":
			reply(`{"status":"unsubscribed"}`)
		case f.Method == "turn/interrupt" || f.Type == "control_request" && f.Request.Subtype == "interrupt":
			if scenario == "session-error-result" {
				return nil
			}
			if scenario == "session-model-change" || scenario == "session-thinking-change" {
				if provider == "codex" {
					raw(`{"method":"thread/settings/updated","params":{"threadId":"root","threadSettings":{"model":"observed-model","effort":"low","approvalPolicy":"never","approvalsReviewer":"user","collaborationMode":{"mode":"default","settings":{"model":"observed-model"}},"cwd":"/synthetic","modelProvider":"synthetic","sandboxPolicy":{"type":"readOnly"}}}}`)
				} else {
					emit(map[string]any{"type": "system", "subtype": "init", "session_id": root, "claude_code_version": "2.1.258", "model": "observed-model"})
				}
			}

			interrupts++
			if scenario == "session-tool-completion" {
				complete(false)
			}
			if scenario == "session-canceled-approval" && interrupts == 1 {
				if provider == "codex" {
					raw(`{"method":"serverRequest/resolved","params":{"threadId":"root","requestId":"first"}}`)
				} else {
					emit(map[string]any{"type": "control_cancel_request", "request_id": "first"})
				}
			}
			marker("interrupt", nil)
			reply(`{}`)

		case f.Method == "thread/settings/update" || f.Type == "control_request" && (f.Request.Subtype == "set_model" || f.Request.Subtype == "set_max_thinking_tokens"):
			var q struct{ Model string }
			source := f.Params
			if provider == "claude" {
				var w struct{ Request json.RawMessage }
				_ = json.Unmarshal(line, &w)
				source = w.Request
			}
			if err = json.Unmarshal(source, &q); err != nil {
				return err
			}
			if q.Model == "native-reject" {
				if provider == "codex" {
					raw(fmt.Sprintf(`{"id":%s,"error":{"code":-32602,"message":"native restriction","data":{"future":42}}}`, f.ID))
				} else {
					raw(fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"native restriction","future":42}}`, f.RequestID))
				}
			} else {
				reply(`{}`)
			}

		case f.Method == "thread/list":
			var q struct {
				Cursor   string
				Archived bool
			}
			if err = json.Unmarshal(f.Params, &q); err != nil {
				return err
			}
			id, cursor := "stored-a", `"p2"`
			if q.Cursor != "" {
				id, cursor = "stored-b", `null`
			}
			if q.Archived {
				id = "archived"
				cursor = `null`
			}
			reply(fmt.Sprintf(`{"data":[{"id":%q,"sessionId":"stored","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":false,"modelProvider":"synthetic","preview":"stored","projectId":null,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root","depth":1}}},"status":{"type":"idle"},"turns":[],"future":null}],"nextCursor":%s}`, id, cursor))
		case f.Method == "fixture/emit":
			var frames []json.RawMessage
			if err = json.Unmarshal(f.Params, &frames); err != nil {
				return err
			}
			for _, b := range frames {
				raw(string(b))
			}
			reply(`{}`)
		case f.Method == "turn/start" || f.Type == "user":
			inputs++
			target := root
			if provider == "codex" {
				var q struct{ ThreadID string }
				_ = json.Unmarshal(f.Params, &q)
				target = q.ThreadID
			}
			if target == root {
				marker("submitted", map[string]any{"count": inputs, "wire": json.RawMessage(line)})
			} else {
				emit(map[string]any{"method": "fixture/submitted", "params": map[string]any{"threadId": target, "count": inputs, "wire": json.RawMessage(line)}})
			}
			if scenario == "session-close" {
				return nil
			}
			if scenario == "session-slow-consumer" && provider == "codex" {
				continue
			}

			if provider == "codex" && scenario == "session-submit-rejection" && inputs == 3 {
				raw(fmt.Sprintf(`{"id":%s,"error":{"code":-32001,"message":"native busy refusal","data":{"future":42}}}`, f.ID))
				continue
			}
			if provider == "codex" {
				reply(`{"turn":{"id":"turn","status":"inProgress","items":[],"error":null}}`)
			}
			if scenario == "session-slow-consumer" && provider == "claude" {
				var first [1]byte
				if _, err = io.ReadFull(os.Stdin, first[:]); err != nil {
					return err
				}
				conn, err := net.DialTimeout("tcp4", os.Getenv("SDK_INTEGRATION_BARRIER"), 5*time.Second)
				if err != nil {
					return err
				}
				defer conn.Close()
				if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return err
				}
				if _, err = conn.Write(first[:]); err != nil {
					return err
				}
				var release [1]byte
				if _, err = io.ReadFull(conn, release[:]); err != nil {
					return err
				}
				if release[0] != 'r' {
					return fmt.Errorf("invalid fault release")
				}
				return nil
			}
			switch scenario {
			case "session-interrupt", "session-error-result":
				if scenario == "session-error-result" {
					request("pending", false)
					complete(true)
				}
				n := 256
				if scenario == "session-error-result" {
					n = 32
				}
				for i := range n {
					marker("burst", map[string]any{"sequence": i})
				}
			case "session-persistent-inputs":
				complete(false)
			case "session-submit-rejection":
				if provider == "claude" && inputs == 2 {
					complete(true)
				}
			case "session-tool-completion":
				if provider == "codex" {
					raw(`{"method":"item/completed","params":{"threadId":"root","turnId":"turn","completedAtMs":1,"item":{"type":"commandExecution","id":"tool","command":"fixture","cwd":"/tmp","processId":null,"status":"completed","commandActions":[],"aggregatedOutput":"ok","exitCode":0,"durationMs":1}}}`)
				} else {
					emit(map[string]any{"type": "user", "session_id": root, "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tool", "content": "ok"}}}})
				}
			case "session-child-association", "session-child-depth":
				if provider == "codex" {
					raw(`{"method":"future/early","params":{"threadId":"unknown"}}`)
					for _, pair := range [][2]string{{"child", "root"}, {"grand", "child"}, {"sibling", "root"}} {
						var thread map[string]json.RawMessage
						_ = json.Unmarshal(nativeScenarioThread(pair[0]), &thread)
						thread["parentThreadId"], _ = json.Marshal(pair[1])
						emit(map[string]any{"method": "thread/started", "params": map[string]any{"thread": thread}})
					}
					for _, id := range []string{"grand", "child", "sibling"} {
						raw(fmt.Sprintf(`{"method":"item/agentMessage/delta","params":{"threadId":%q,"turnId":"turn","itemId":"text","delta":"visible"}}`, id))
					}
					raw(`{"method":"item/completed","params":{"threadId":"root","turnId":"turn","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":"send","tool":"sendInput","status":"completed","senderThreadId":"root","receiverThreadIds":["external"],"agentsStates":{}}}}`)
					raw(`{"method":"future/external","params":{"threadId":"external"}}`)
					complete(false)
					raw(`{"method":"item/agentMessage/delta","params":{"threadId":"grand","turnId":"turn","itemId":"text","delta":"after root"}}`)
				} else {
					task := func(id, tool string) {
						emit(map[string]any{"type": "system", "subtype": "task_started", "session_id": root, "task_type": "local_agent", "task_id": id, "tool_use_id": tool})
					}
					msg := func(tool string, content any) {
						emit(map[string]any{"type": "assistant", "session_id": root, "parent_tool_use_id": tool, "message": map[string]any{"content": content}})
					}
					task("child", "spawn-child")
					msg("spawn-child", []any{map[string]any{"type": "tool_use", "id": "spawn-grand", "name": "Agent", "input": map[string]any{}}})
					task("grand", "spawn-grand")
					msg("spawn-grand", "nested")
					emit(map[string]any{"type": "assistant", "session_id": root, "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "spawn-child", "name": "Agent", "input": map[string]any{}}, map[string]any{"type": "tool_use", "id": "spawn-sibling", "name": "Agent", "input": map[string]any{}}}}})
					task("sibling", "spawn-sibling")
					msg("spawn-sibling", "parallel")
					complete(false)
					msg("spawn-grand", "after root")
					emit(map[string]any{"type": "assistant", "session_id": root, "agent_id": "external", "message": map[string]any{"content": "unverified communication identity"}})
					emit(map[string]any{"type": "assistant", "session_id": "foreign", "message": map[string]any{"content": "must stay isolated"}})
				}
				marker("barrier", nil)
			case "session-approval", "session-canceled-approval", "session-question":
				request("first", scenario == "session-question")
				request("second", scenario == "session-question")

			case "session-unknown-events":
				for range 2 {
					if provider == "codex" {
						raw(`{"method":"future/repeated","params":{"threadId":"root","itemId":"same","future":{"n":9007199254740993,"nullable":null}},"extra":true}`)
					} else {
						raw(fmt.Sprintf(`{"type":"system","subtype":"status","fixture":"future_repeated","session_id":%q,"uuid":"33333333-3333-4333-8333-333333333333","future":{"n":9007199254740993,"nullable":null}}`, root))
					}
				}
				if provider == "claude" {
					emit(map[string]any{"type": "future_envelope", "session_id": root, "future": 42})
				}
				marker("barrier", nil)
			}
		case provider == "codex" && len(f.ID) > 0 && f.Method == "" || provider == "claude" && f.Type == "control_response":
		// The original native answer is recorded verbatim; no input or second answer.
		default:
			return fmt.Errorf("unexpected native fixture request: %s", line)
		}
	}
	return scanner.Err()
}

func nativeScenarioThread(id string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"sessionId":"native-session","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic","ephemeral":false,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]}`, id))
}

// Literal defaults for REQUIRED fixed TS SDK 0.3.258 envelopes. This helper
// constructs synthetic peer output only; it does not repair SDK input/output.
// Values below are original fixture data, not copied upstream implementation.
var claudeFixtureSequence uint64

func completeClaudeFixture(raw json.RawMessage) json.RawMessage {
	claudeFixtureSequence++
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil {
		return raw
	}
	var kind, sub string
	_ = json.Unmarshal(f["type"], &kind)
	_ = json.Unmarshal(f["subtype"], &sub)
	put := func(key, value string) {
		if _, ok := f[key]; !ok {
			f[key] = json.RawMessage(value)
		}
	}
	switch kind {
	case "system", "assistant", "result", "stream_event":
		put("uuid", fmt.Sprintf(`"22222222-2222-4222-8222-%012d"`, claudeFixtureSequence))
	}
	switch kind {
	case "system":
		switch sub {
		case "init":
			for k, v := range map[string]string{"apiKeySource": `"none"`, "claude_code_version": `"2.1.258"`, "cwd": `"/synthetic"`, "tools": `[]`, "mcp_servers": `[]`, "model": `"synthetic"`, "permissionMode": `"default"`, "slash_commands": `[]`, "output_style": `"default"`, "skills": `[]`, "plugins": `[]`} {
				put(k, v)
			}
		case "status":
			put("status", `null`)
		case "task_started":
			put("description", `"synthetic task"`)
		}
	case "result":
		for k, v := range map[string]string{"duration_ms": `1`, "duration_api_ms": `1`, "is_error": `false`, "num_turns": `1`, "stop_reason": `"end_turn"`, "total_cost_usd": `0`, "usage": `{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"inference_geo":"not_available","iterations":[],"speed":"standard"}`, "modelUsage": `{}`, "permission_denials": `[]`} {
			put(k, v)
		}
	case "assistant", "user":
		put("parent_tool_use_id", `null`)
		var msg map[string]json.RawMessage
		_ = json.Unmarshal(f["message"], &msg)
		if msg != nil {
			defaults := map[string]string{"role": fmt.Sprintf("%q", kind)}
			if kind == "assistant" {
				defaults["id"] = `"msg-synthetic"`
				defaults["type"] = `"message"`
				defaults["model"] = `"synthetic"`
				defaults["stop_reason"] = `null`
				defaults["stop_sequence"] = `null`
				defaults["usage"] = `{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}`
				var text string
				if json.Unmarshal(msg["content"], &text) == nil {
					msg["content"], _ = json.Marshal([]any{map[string]any{"type": "text", "text": text}})
				}
			}
			for k, v := range defaults {
				if _, ok := msg[k]; !ok {
					msg[k] = json.RawMessage(v)
				}
			}
			f["message"], _ = json.Marshal(msg)
		}
	case "stream_event":
		put("parent_tool_use_id", `null`)
	}
	b, err := json.Marshal(f)
	if err != nil {
		return raw
	}
	return b
}
