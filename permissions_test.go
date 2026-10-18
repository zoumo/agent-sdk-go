// SPDX-License-Identifier: Apache-2.0

package claude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// Original public subprocess tests, behaviorally informed by the pinned official
// Python tests/test_tool_callbacks.py (allow, deny, modified input, tool-use IDs,
// display context, callback errors) and tests/test_query.py (permission roundtrip
// before stdin closure and control cancellation). These fixtures add the Go
// design's retained handles, bounded workers and single-use guarantees. They do
// not establish live CLI compatibility or an AskUserQuestion answer schema.
func runPermissionFixture(mode string, reader *bufio.Reader) {
	var transcript *os.File
	if path := os.Getenv("CLAUDE_GO_TRANSCRIPT"); path != "" {
		var err error
		transcript, err = os.Create(path)
		if err != nil {
			os.Exit(12)
		}
		defer transcript.Close()
	}
	var frames []json.RawMessage
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if transcript != nil {
			if _, err := transcript.Write(line); err != nil {
				os.Exit(13)
			}
		}
		frames = append(frames, append(json.RawMessage(nil), line...))
		var wire struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Message   struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &wire) != nil {
			os.Exit(14)
		}
		switch wire.Type {
		case "control_response":
			fixtureEmit(map[string]any{"type": "reply_seen", "wire": json.RawMessage(line)})
		case "control_request":
			sessionReply(wire.RequestID, "")
		case "user":
			command, value, _ := strings.Cut(wire.Message.Content, ":")
			switch command {
			case "emit":
				fixtureEmit(json.RawMessage(value))
			case "block_write":
				frame, keepGoing := permissionBlockedWire(reader, transcript, value)
				if !keepGoing {
					return
				}
				frames = append(frames, frame)
			case "snapshot":
				fixtureEmit(map[string]any{"type": "snapshot", "frames": frames})
			case "disconnect":
				return
			case "late_control_on_close":
				fixtureEmit(map[string]any{"type": "ready"})
				for {
					if _, err := reader.ReadBytes('\n'); err != nil {
						break
					}
				}
				fixtureEmit(map[string]any{"type": "control_request", "request_id": "late", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
				return
			default:
				os.Exit(15)
			}
		}
	}
}

func emitPermissionFrame(t *testing.T, c *claude.Client, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, "emit:"+string(data))
}

func emitPermission(t *testing.T, c *claude.Client, id, tool string, input json.RawMessage) claude.Event {
	t.Helper()
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": tool, "input": input, "tool_use_id": "tool-" + id,
		"agent_id": "agent-context", "permission_suggestions": []any{map[string]any{"future_rule": true}},
		"blocked_path": "outside/project", "decision_reason": "host approval", "title": "Approval question",
		"display_name": "Tool action", "description": "Review input", "future_context": map[string]any{"kept": 7},
	}})
	event := receive(t, c)
	if event.Control == nil || event.Control.RequestID != id {
		t.Fatalf("expected permission %q, got %s", id, event.Raw)
	}
	return event
}

func awaitPermission(t *testing.T, requests <-chan *claude.PermissionRequest) *claude.PermissionRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("permission handler was not dispatched")
		return nil
	}
}

func awaitPermissionError(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("permission answer did not finish")
		return nil
	}
}

func permissionSnapshot(t *testing.T, c *claude.Client) []json.RawMessage {
	t.Helper()
	send(t, c, "snapshot")
	event := receive(t, c)
	if event.Type != "snapshot" {
		t.Fatalf("unexpected unsolicited output before snapshot: %s", event.Raw)
	}
	var snapshot struct {
		Frames []json.RawMessage `json:"frames"`
	}
	if err := json.Unmarshal(event.Raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot.Frames
}

func permissionResponses(t *testing.T, frames []json.RawMessage) []map[string]any {
	t.Helper()
	var responses []map[string]any
	for _, frame := range frames {
		var wire map[string]any
		if err := json.Unmarshal(frame, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["type"] == "control_response" {
			responses = append(responses, wire)
		}
		if wire["type"] == "user" {
			message := wire["message"].(map[string]any)
			if _, ok := message["content"].(string); !ok {
				t.Fatalf("invented tool result: %s", frame)
			}
		}
	}
	return responses
}

func assertPermissionReply(t *testing.T, c *claude.Client, id string, decision map[string]any) {
	t.Helper()
	event := receive(t, c)
	var echoed struct {
		Wire map[string]any `json:"wire"`
	}
	if event.Type != "reply_seen" || json.Unmarshal(event.Raw, &echoed) != nil {
		t.Fatalf("expected response echo: %s", event.Raw)
	}
	want := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": decision}}
	if !reflect.DeepEqual(echoed.Wire, want) {
		t.Fatalf("response mismatch:\ngot  %#v\nwant %#v", echoed.Wire, want)
	}
}

func permissionTranscript(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var frames []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			frames = append(frames, json.RawMessage(line))
		}
	}
	return frames
}

func TestDeferredPermissionHumanWaitKeepsTransportMoving(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	answers := make(chan error, 2)
	releaseHuman := make(chan struct{})
	handlerReturned := make(chan struct{}, 2)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.PermissionWorkers = 2
		opts.PermissionHandler = func(p *claude.PermissionRequest) {
			defer func() { handlerReturned <- struct{}{} }()
			requests <- p
			if p.RequestID() == "human" {
				select {
				case <-releaseHuman:
				case <-p.Context().Done():
					return
				}
				answers <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow})
			} else {
				answers <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "Not approved\n理由"})
			}
		}
	})
	event := emitPermission(t, c, "human", "Read", json.RawMessage(`{"path":"review.txt","unknown":[1,true]}`))
	human := awaitPermission(t, requests)
	if human.RequestID() != "human" || human.ToolUseID() != "tool-human" || human.ToolName() != "Read" || human.Epoch() == 0 {
		t.Fatal("permission identifiers lost")
	}
	if human.Context().Err() != nil {
		t.Fatal("fresh handle already cancelled")
	}
	var raw map[string]any
	if err := json.Unmarshal(human.Raw(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["agent_id"] != "agent-context" || raw["decision_reason"] != "host approval" || raw["future_context"].(map[string]any)["kept"] != float64(7) {
		t.Fatalf("context lost: %s", human.Raw())
	}
	// Returned bytes and public event metadata cannot rewrite the authority or
	// original input used by a later allow.
	for _, data := range [][]byte{human.Input(), human.Raw(), event.Control.Request} {
		for i := range data {
			data[i] = ' '
		}
	}
	event.Control.RequestID = "tampered"
	emitPermissionFrame(t, c, map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "still moving"}}})
	if receive(t, c).Stream == nil {
		t.Fatal("human wait blocked stream event")
	}
	emitPermission(t, c, "independent", "Write", json.RawMessage(`{"path":"other.txt"}`))
	second := awaitPermission(t, requests)
	if second.RequestID() != "independent" || second.Epoch() != human.Epoch() {
		t.Fatal("second permission was not independent")
	}
	if err := awaitPermissionError(t, answers); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "independent", map[string]any{"behavior": "deny", "message": "Not approved\n理由"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Interrupt(ctx); err != nil {
		t.Fatalf("human wait blocked control: %v", err)
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("early response: %#v", got)
	}
	close(releaseHuman)
	if err := awaitPermissionError(t, answers); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "human", map[string]any{"behavior": "allow", "updatedInput": map[string]any{"path": "review.txt", "unknown": []any{float64(1), true}}})
	for range 2 {
		select {
		case <-handlerReturned:
		case <-time.After(3 * time.Second):
			t.Fatal("handler did not return")
		}
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 2 {
		t.Fatalf("duplicate handler-return response: %#v", got)
	}
}

func TestDeferredPermissionRetainedHandleUsesExactUpdatedInput(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		updated    json.RawMessage
		want       map[string]any
	}{
		{"original", "Read", nil, map[string]any{"original": true}},
		{"empty object", "Read", json.RawMessage(`{}`), map[string]any{}},
		{"updated", "Write", json.RawMessage(`{"replacement":"safe","future":7}`), map[string]any{"replacement": "safe", "future": float64(7)}},
		{"question", "AskUserQuestion", json.RawMessage(`{"questions":[{"question":"Which format?","options":[]}],"answers":{"Which format?":"user-supplied"}}`), map[string]any{"questions": []any{map[string]any{"question": "Which format?", "options": []any{}}}, "answers": map[string]any{"Which format?": "user-supplied"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan *claude.PermissionRequest, 1)
			returned := make(chan struct{})
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p; close(returned) }
			})
			emitPermission(t, c, "original/request-ID", tc.tool, json.RawMessage(`{"original":true}`))
			p := awaitPermission(t, requests)
			<-returned
			if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 0 {
				t.Fatalf("handler return answered: %#v", got)
			}
			if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: tc.updated}); err != nil {
				t.Fatal(err)
			}
			assertPermissionReply(t, c, "original/request-ID", map[string]any{"behavior": "allow", "updatedInput": tc.want})
			copyOfHandle := *p
			if err := copyOfHandle.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); !errors.Is(err, claude.ErrPermissionUsed) {
				t.Fatalf("copy reused authority: %v", err)
			}
			if !errors.Is(context.Cause(p.Context()), claude.ErrPermissionUsed) {
				t.Fatal("completed handle context remained live")
			}
			if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
				t.Fatalf("duplicate response: %#v", got)
			}
		})
	}
}

func TestAssistantQuestionIsOnlyAnEvent(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.PermissionWorkers = 1
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermissionFrame(t, c, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "question-tool", "name": "AskUserQuestion", "input": map[string]any{"questions": []any{}}}}}})
	event := receive(t, c)
	if event.Assistant == nil || event.Assistant.Content.Blocks[0].ToolUse.Name != "AskUserQuestion" {
		t.Fatalf("question event lost: %s", event.Raw)
	}
	// One worker and a later real request form a dispatch-order barrier. A
	// mistakenly enqueued assistant handle would arrive before this real one.
	emitPermission(t, c, "real-control", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	if p.RequestID() != "real-control" {
		t.Fatal("assistant block created an answer authority")
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "real-control", map[string]any{"behavior": "deny", "message": ""})
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("assistant block caused response: %#v", got)
	}
}

func TestPermissionCancellationImmediatelyInvalidatesHandle(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "abandoned", "AskUserQuestion", json.RawMessage(`{"questions":[]}`))
	p := awaitPermission(t, requests)
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "abandoned", "future": "preserved"})
	cancelEvent := receive(t, c)
	if cancelEvent.Type != "control_cancel_request" || !strings.Contains(string(cancelEvent.Raw), `"future":"preserved"`) {
		t.Fatalf("cancel frame lost: %s", cancelEvent.Raw)
	}
	if !errors.Is(context.Cause(p.Context()), claude.ErrPermissionCancelled) {
		t.Fatal("observed cancel did not invalidate context immediately")
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrPermissionCancelled) {
		t.Fatalf("canceled answer: %v", err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "unknown"})
	if receive(t, c).Type != "control_cancel_request" {
		t.Fatal("unknown cancellation was hidden")
	}
	emitPermission(t, c, "still-live", "Read", json.RawMessage(`{}`))
	other := awaitPermission(t, requests)
	if err := other.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "no"}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "still-live", map[string]any{"behavior": "deny", "message": "no"})
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("cancellation produced response: %#v", got)
	}
}

func TestPermissionInvalidAnswerAndPanicFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		answer claude.PermissionAnswer
		panic  bool
	}{
		{name: "panic", panic: true},
		{name: "nil panic", panic: true},
		{name: "missing behavior"},
		{name: "unknown behavior", answer: claude.PermissionAnswer{Behavior: "ask"}},
		{name: "invalid JSON", answer: claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage(`{`)}},
		{name: "null", answer: claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage(`null`)}},
		{name: "array", answer: claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage(`[]`)}},
		{name: "empty bytes", answer: claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: json.RawMessage{}}},
		{name: "allow reason", answer: claude.PermissionAnswer{Behavior: claude.PermissionAllow, Message: "contradictory"}},
		{name: "deny input", answer: claude.PermissionAnswer{Behavior: claude.PermissionDeny, UpdatedInput: json.RawMessage(`{}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "nil panic" {
				t.Setenv("GODEBUG", "panicnil=1")
			}
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			results := make(chan error, 1)
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *claude.PermissionRequest) {
					if tc.panic {
						if tc.name == "nil panic" {
							panic(nil)
						}
						panic("fixture handler panic")
					}
					results <- p.Answer(context.Background(), tc.answer)
				}
			})
			// A terminal handler failure may beat receiving its accepted event.
			emitPermission(t, c, "bad-handler", "Read", json.RawMessage(`{}`))
			want := claude.ErrInvalidPermissionAnswer
			if tc.panic {
				want = claude.ErrPermissionHandlerPanic
			} else if err := awaitPermissionError(t, results); !errors.Is(err, want) {
				t.Fatalf("invalid answer error: %v", err)
			}
			awaitDone(t, c)
			if err := c.Close(); !errors.Is(err, want) {
				t.Fatalf("failure was not observable: %v", err)
			}
			var failure *claude.PermissionError
			if !errors.As(c.Err(), &failure) || failure.RequestID != "bad-handler" {
				t.Fatalf("missing request-specific failure: %v", c.Err())
			}
			if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
				t.Fatalf("failed handler granted/responded: %#v", got)
			}
		})
	}
}

func TestPermissionAnswerConcurrentSingleUse(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "one-response", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	start := make(chan struct{})
	results := make(chan error, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: fmt.Sprint(i)})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, claude.ErrPermissionUsed) {
			t.Fatalf("concurrent answer: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful responses=%d", successes)
	}
	if receive(t, c).Type != "reply_seen" {
		t.Fatal("response missing")
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("duplicate responses: %#v", got)
	}
}

func TestPermissionCloseInvalidatesAndFreshClientHasNewEpoch(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 2)
	configure := func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	}
	first := openFixture(t, "permission_controls", configure)
	emitPermission(t, first, "reused-id", "Read", json.RawMessage(`{}`))
	old := awaitPermission(t, requests)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(context.Cause(old.Context()), claude.ErrClosed) {
		t.Fatal("Close did not cancel pending context")
	}
	second := openFixture(t, "permission_controls", configure)
	emitPermission(t, second, "reused-id", "Read", json.RawMessage(`{}`))
	fresh := awaitPermission(t, requests)
	if old.Epoch() == fresh.Epoch() {
		t.Fatal("connection epoch reused")
	}
	if err := old.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrClosed) {
		t.Fatalf("old answer: %v", err)
	}
	if err := fresh.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, second, "reused-id", map[string]any{"behavior": "deny", "message": ""})
	if got := permissionResponses(t, permissionSnapshot(t, second)); len(got) != 1 {
		t.Fatalf("stale response crossed client: %#v", got)
	}
}

func TestPermissionWorkerBoundAndCloseDoNotWaitForUserCode(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 3)
	release := make(chan struct{})
	returned := make(chan struct{})
	defer close(release)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.PermissionWorkers = 1
		opts.Limits.ControlBuffer = 1
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p; <-release; close(returned) }
	})
	emitPermission(t, c, "running", "Read", json.RawMessage(`{}`))
	running := awaitPermission(t, requests)
	emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`))
	if len(requests) != 0 {
		t.Fatal("worker limit exceeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "overflow", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
	awaitDone(t, c)
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	if err := awaitPermissionError(t, closed); !errors.Is(err, claude.ErrResourceLimit) {
		t.Fatalf("overflow: %v", err)
	}
	if running.Context().Err() == nil {
		t.Fatal("overflow left running permission context live")
	}
	select {
	case <-returned:
		t.Fatal("test handler unexpectedly returned before release")
	default:
	}
}

func TestPermissionDuplicateIDCannotCreateSecondAuthority(t *testing.T) {
	for _, subtype := range []string{"can_use_tool", "unsupported"} {
		t.Run(subtype, func(t *testing.T) {
			requests := make(chan *claude.PermissionRequest, 2)
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "duplicate", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "duplicate", "request": map[string]any{"subtype": subtype, "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			if err := c.Close(); !errors.Is(err, claude.ErrProtocol) {
				t.Fatalf("duplicate ID not rejected: %v", err)
			}
			if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrClosed) {
				t.Fatalf("duplicate invalidation: %v", err)
			}
			if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 0 {
				t.Fatalf("duplicate created responder: %#v", got)
			}
		})
	}
}

func TestPermissionLateValidControlDoesNotMakeCloseProtocolError(t *testing.T) {
	c := openFixture(t, "permission_controls")
	send(t, c, "late_control_on_close")
	if receive(t, c).Type != "ready" {
		t.Fatal("fixture not ready")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("valid shutdown frame became error: %v", err)
	}
	if event := receive(t, c); event.Control == nil || event.Control.RequestID != "late" {
		t.Fatalf("late control was not retained: %s", event.Raw)
	}
	if _, err := c.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("shutdown stream end: %v", err)
	}
}

func TestPermissionCanceledCallerContextDoesNotConsumeHandle(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "retry-before-reserve", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, context.Canceled) || errors.Is(err, claude.ErrUnknownOutcome) {
		t.Fatalf("already canceled answer: %v", err)
	}
	if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
		t.Fatal(err)
	}
	assertPermissionReply(t, c, "retry-before-reserve", map[string]any{"behavior": "deny", "message": ""})
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 1 {
		t.Fatalf("canceled call wrote: %#v", got)
	}
}

func TestPermissionQueuedCancellationNeverInvokesHandler(t *testing.T) {
	requests := make(chan *claude.PermissionRequest, 3)
	release := make(chan struct{})
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Limits.PermissionWorkers = 1
		opts.PermissionHandler = func(p *claude.PermissionRequest) {
			requests <- p
			if p.RequestID() == "first" {
				select {
				case <-release:
				case <-p.Context().Done():
				}
			}
		}
	})
	emitPermission(t, c, "first", "Read", json.RawMessage(`{}`))
	first := awaitPermission(t, requests)
	emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`))
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "queued"})
	if receive(t, c).Type != "control_cancel_request" {
		t.Fatal("cancel missing")
	}
	// The independent automatic responder remains available while every user
	// worker is occupied.
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "unsupported", "request": map[string]any{"subtype": "future_control"}})
	if receive(t, c).Control == nil {
		t.Fatal("unsupported event missing")
	}
	echo := receive(t, c)
	var reply struct {
		Wire map[string]any `json:"wire"`
	}
	if echo.Type != "reply_seen" || json.Unmarshal(echo.Raw, &reply) != nil || !reflect.DeepEqual(reply.Wire, map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": "unsupported", "error": "Unsupported control request subtype: future_control"}}) {
		t.Fatalf("independent unsupported response: %s", echo.Raw)
	}
	close(release)
	emitPermission(t, c, "last", "Read", json.RawMessage(`{}`))
	last := awaitPermission(t, requests)
	if last.RequestID() != "last" {
		t.Fatalf("canceled queued handler invoked: %s", last.RequestID())
	}
	for _, p := range []*claude.PermissionRequest{first, last} {
		if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
			t.Fatal(err)
		}
		assertPermissionReply(t, c, p.RequestID(), map[string]any{"behavior": "deny", "message": ""})
	}
	if got := permissionResponses(t, permissionSnapshot(t, c)); len(got) != 3 {
		t.Fatalf("queued cancel answered: %#v", got)
	}
}

func TestPermissionPanicAfterAnswerDoesNotCreateSecondResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	results := make(chan error, 1)
	c := openFixture(t, "permission_controls", func(opts *claude.Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *claude.PermissionRequest) {
			results <- p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "explicit"})
			panic("after response")
		}
	})
	emitPermission(t, c, "answered-before-panic", "Read", json.RawMessage(`{}`))
	if err := awaitPermissionError(t, results); err != nil {
		t.Fatal(err)
	}
	awaitDone(t, c)
	if err := c.Close(); !errors.Is(err, claude.ErrPermissionHandlerPanic) {
		t.Fatal(err)
	}
	if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != 1 {
		t.Fatalf("panic duplicated response: %#v", got)
	}
}

func TestPermissionZeroHandleCannotAnswer(t *testing.T) {
	for _, p := range []*claude.PermissionRequest{nil, new(claude.PermissionRequest)} {
		if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionAllow}); !errors.Is(err, claude.ErrInvalidPermissionRequest) {
			t.Fatalf("zero handle: %v", err)
		}
		if !errors.Is(context.Cause(p.Context()), claude.ErrInvalidPermissionRequest) {
			t.Fatal("zero context live")
		}
	}
}

func TestIncomingControlHistoryRejectsRetiredIDReuse(t *testing.T) {
	for _, previous := range []string{"unsupported", "answered", "cancelled", "unknown cancellation"} {
		t.Run(previous, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			requests := make(chan *claude.PermissionRequest, 2)
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
			})
			wantResponses := 0
			switch previous {
			case "unsupported":
				emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "retired", "request": map[string]any{"subtype": "unsupported"}})
				if receive(t, c).Control == nil || receive(t, c).Type != "reply_seen" {
					t.Fatal("unsupported request did not finish")
				}
				wantResponses = 1
			case "answered", "cancelled":
				emitPermission(t, c, "retired", "Read", json.RawMessage(`{}`))
				p := awaitPermission(t, requests)
				if previous == "answered" {
					if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
						t.Fatal(err)
					}
					assertPermissionReply(t, c, "retired", map[string]any{"behavior": "deny", "message": ""})
					wantResponses = 1
				} else {
					emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "retired"})
					if receive(t, c).Type != "control_cancel_request" {
						t.Fatal("cancel missing")
					}
				}
			case "unknown cancellation":
				emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "retired"})
				if receive(t, c).Type != "control_cancel_request" {
					t.Fatal("cancel missing")
				}
			}
			subtype := "can_use_tool"
			if previous == "answered" || previous == "cancelled" {
				subtype = "unsupported"
			}
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "retired", "request": map[string]any{"subtype": subtype, "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			if err := c.Close(); !errors.Is(err, claude.ErrProtocol) {
				t.Fatalf("retired ID revived: %v", err)
			}
			if got := permissionResponses(t, permissionTranscript(t, path)); len(got) != wantResponses {
				t.Fatalf("retired ID responded again: %#v", got)
			}
		})
	}
}

func TestPermissionRegistryBoundsFailClosed(t *testing.T) {
	for _, bound := range []string{"pending", "history"} {
		t.Run(bound, func(t *testing.T) {
			requests := make(chan *claude.PermissionRequest, 2)
			c := openFixture(t, "permission_controls", func(opts *claude.Options) {
				if bound == "pending" {
					opts.Limits.PendingPermissions = 1
				} else {
					opts.Limits.PermissionHistory = 1
				}
				opts.PermissionHandler = func(p *claude.PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "first", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			if bound == "history" {
				if err := p.Answer(context.Background(), claude.PermissionAnswer{Behavior: claude.PermissionDeny}); err != nil {
					t.Fatal(err)
				}
				assertPermissionReply(t, c, "first", map[string]any{"behavior": "deny", "message": ""})
			}
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "excess", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			if err := c.Close(); !errors.Is(err, claude.ErrResourceLimit) {
				t.Fatalf("registry overflow: %v", err)
			}
			if p.Context().Err() == nil {
				t.Fatal("overflow left pending context live")
			}
		})
	}
}
