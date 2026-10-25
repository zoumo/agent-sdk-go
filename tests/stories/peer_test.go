package stories_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
)

func TestMain(m *testing.M) {
	if provider := os.Getenv("SDK_STORY_PEER"); provider != "" {
		if err := peer(provider, os.Getenv("SDK_STORY")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// This self-reexecuting peer understands only fixture traffic. It never opens an
// external connection, loads account configuration, or starts a native agent.
// Fault companions use only a loopback application synchronization channel.
func peer(provider, story string) error {
	if provider == "codex" && story == "recorded-question-drain" {
		frames, err := recording.Load("s2")
		if err != nil {
			return err
		}
		return recording.Peer(os.Stdin, os.Stdout, frames)
	}
	if provider == "claude" && story == "recorded-auth-drain" {
		return transcript.Replay(os.Stdin, os.Stdout, bytes.NewReader(transcript.Wire), func() error {
			c, err := net.DialTimeout("tcp4", os.Getenv("SDK_STORY_BARRIER"), 5*time.Second)
			if err != nil {
				return err
			}
			defer c.Close()
			if err = c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return err
			}
			_, err = c.Write([]byte("d"))
			return err
		})
	}
	if story == "US-014" && provider == "codex" {
		return historyQueryStoryPeer()
	}
	if strings.HasPrefix(story, "SDK-US-") {
		return sessionTargetPeer(provider, story)
	}
	scanner := bufio.NewScanner(os.Stdin)
	inputs, answers := 0, 0
	emit := func(method, params string) {
		if provider == "codex" {
			fmt.Fprintf(os.Stdout, `{"method":%q,"params":%s}`+"\n", method, params)
		} else {
			fmt.Fprintln(os.Stdout, string(completeClaudeFixture(json.RawMessage(params))))
		}
	}
	marker := func(kind string) {
		if provider == "codex" {
			emit("fixture/"+kind, `{"threadId":"root"}`)
		} else {
			emit("", `{"type":"system","subtype":"status","fixture":"fixture_`+kind+`","session_id":"s"}`)
		}
	}
	complete := func() {
		if provider == "codex" {
			emit("turn/completed", `{"threadId":"root","turn":{"id":"turn","items":[],"status":"completed","error":null}}`)
		} else {
			emit("", `{"type":"result","subtype":"success","session_id":"s","result":"done"}`)
		}
	}
	association := func(child, parent string) {
		if provider == "codex" {
			emit("thread/started", nativeParentFixture(child, parent))
			return
		}
		// A root-owned Agent invocation is evidence for root ownership; nested
		// task_started frames carry an explicit parent_task_id.
		if parent == "root" {
			emit("", `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn-`+child+`","name":"Agent","input":{}}]}}`)
			emit("", `{"type":"system","subtype":"task_started","session_id":"s","task_id":"`+child+`","task_type":"local_agent","tool_use_id":"spawn-`+child+`"}`)
		} else {
			emit("", `{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn-`+parent+`","message":{"content":[{"type":"tool_use","id":"spawn-`+child+`","name":"Agent","input":{}}]}}`)
			emit("", `{"type":"system","subtype":"task_started","session_id":"s","task_id":"`+child+`","task_type":"local_agent","tool_use_id":"spawn-`+child+`"}`)
		}
	}
	native := func(child string) {
		if provider == "codex" {
			emit("item/agentMessage/delta", `{"threadId":"`+child+`","turnId":"turn","itemId":"text","delta":"child text"}`)
		} else {
			emit("", `{"type":"assistant","session_id":"s","agent_id":"`+child+`","parent_tool_use_id":"spawn-`+child+`","message":{"content":[{"type":"text","text":"child text"}]}}`)
		}
	}
	snapshot := func(wire json.RawMessage) {
		if provider == "codex" {
			emit("fixture/snapshot", fmt.Sprintf(`{"threadId":"root","inputs":%d,"answers":%d,"wire":%s}`, inputs, answers, wire))
		} else {
			emit("", fmt.Sprintf(`{"type":"system","subtype":"status","fixture":"fixture_snapshot","session_id":"s","inputs":%d,"answers":%d,"wire":%s}`, inputs, answers, wire))
		}
	}
	start := func() {
		switch story {
		case "US-001", "US-012":
			marker("barrier")
		case "US-002":
			if provider == "codex" {
				emit("item/completed", `{"threadId":"root","turnId":"turn","completedAtMs":1,"item":{"type":"commandExecution","id":"tool","command":"echo fixture","cwd":"/tmp","processId":null,"status":"completed","commandActions":[],"aggregatedOutput":"fixture","exitCode":0,"durationMs":1}}`)
			} else {
				emit("", `{"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":"fixture"}]}}`)
			}
			marker("barrier")
		case "US-003":
			association("child", "root")
			native("child")
			marker("barrier")
		case "US-004":
			association("child", "root")
			association("sibling", "root")
			association("grand", "child")
			native("unknown")
			marker("barrier")
		case "US-005":
			association("child", "root")
			complete()
			native("child")
			marker("barrier")
		case "US-006", "US-007", "US-008":
			if provider == "codex" {
				method, params := "item/commandExecution/requestApproval", `{"threadId":"root","turnId":"turn","itemId":"tool","command":"echo fixture","cwd":"/tmp","reason":null,"availableDecisions":["accept","decline"],"startedAtMs":1}`
				if story != "US-006" {
					method = "item/tool/requestUserInput"
					params = `{"threadId":"root","turnId":"turn","itemId":"question","isBlocking":false,"questions":[{"id":"q","header":"Choice","question":"Choose?","isOther":true,"isSecret":false,"options":[]}],"autoResolutionMs":null,"future":42}`
				}
				fmt.Fprintf(os.Stdout, `{"id":"original","method":%q,"params":%s}`+"\n", method, params)
			} else {
				tool, input := "Bash", `{"command":"echo fixture"}`
				if story != "US-006" {
					tool = "AskUserQuestion"
					input = `{"questions":[{"question":"Choose?","options":[{"label":"A"}]}],"future":42}`
				}
				emit("", fmt.Sprintf(`{"type":"control_request","session_id":"s","request_id":"original","request":{"subtype":"can_use_tool","tool_name":%q,"tool_use_id":"question","input":%s}}`, tool, input))
			}
		case "US-009":
			for i := 0; i < 32; i++ {
				marker("burst")
			}
			marker("barrier")
		case "US-010":
			for i := 0; i < 2; i++ {
				if provider == "codex" {
					emit("fixture/unknown", `{"threadId":"root","future":{"big":9007199254740993,"nullable":null}}`)
				} else {
					emit("", `{"type":"system","subtype":"status","fixture":"fixture_unknown","session_id":"s","future":{"big":9007199254740993,"nullable":null}}`)
				}
			}
			marker("barrier")
		}
	}
	for scanner.Scan() {
		raw := append(json.RawMessage(nil), scanner.Bytes()...)
		var frame struct {
			ID           json.RawMessage `json:"id"`
			Method, Type string
			RequestID    string `json:"request_id"`
			Request      struct{ Subtype string }
			Message      struct{ Content string }
			Result       json.RawMessage
			Response     json.RawMessage
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			return err
		}
		reply := func(result string) {
			if provider == "codex" {
				fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", frame.ID, result)
			} else {
				emit("", fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":%s}}`, frame.RequestID, result))
			}
		}
		if frame.Method == "initialize" {
			reply(`{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`)
			continue
		}
		if frame.Type == "control_request" && frame.Request.Subtype == "initialize" {
			reply(`{}`)
			emit("", `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
			if story == "US-011" {
				association("child", "root")
				snapshot(json.RawMessage(`null`))
			}
			continue
		}

		if frame.Method == "thread/start" {
			reply(`{"thread":` + string(nativeStoryThread("root")) + `,"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}`)
			continue
		}
		if frame.Method == "thread/unsubscribe" {
			reply(`{"status":"unsubscribed"}`)
			continue
		}
		if frame.Method == "thread/list" {
			var r map[string]json.RawMessage
			_ = json.Unmarshal(nativeStoryThread("child"), &r)
			r["parentThreadId"] = json.RawMessage(`"root"`)
			raw, _ := json.Marshal(r)
			reply(`{"data":[` + string(raw) + `],"nextCursor":null}`)
			continue
		}
		if frame.Method == "initialized" {
			continue
		}
		if frame.Method == "turn/start" || frame.Type == "user" {
			if provider == "claude" && frame.Message.Content == "release" {
				complete()
				marker("end")
				continue
			}
			inputs++
			start()
			if provider == "codex" {
				reply(`{"turn":{"id":"turn","items":[],"status":"inProgress","error":null}}`)
			}
			continue
		}
		if frame.Method == "fixture/release" {
			complete()
			marker("end")
			reply(`{}`)
			continue
		}
		if frame.Method == "fixture/snapshot" {
			snapshot(json.RawMessage(`null`))
			reply(`{}`)
			continue
		}
		if frame.Method == "turn/interrupt" || frame.Type == "control_request" && frame.Request.Subtype == "interrupt" {
			reply(`{}`)
			continue
		}
		if provider == "codex" && len(frame.ID) > 0 && frame.Method == "" || provider == "claude" && frame.Type == "control_response" {
			answers++
			snapshot(raw)
			continue
		}
		return fmt.Errorf("unexpected fixture request: %s", raw)
	}
	return scanner.Err()
}

func nativeParentFixture(child, parent string) string {
	var t map[string]json.RawMessage
	_ = json.Unmarshal(nativeStoryThread(child), &t)
	t["parentThreadId"], _ = json.Marshal(parent)
	b, _ := json.Marshal(map[string]any{"thread": t})
	return string(b)
}
