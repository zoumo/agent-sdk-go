// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func emitPermissionFrame(t *testing.T, c *Client, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	send(t, c, "emit:"+string(data))
}

func emitPermission(t *testing.T, c *Client, id, tool string, input json.RawMessage) Event {
	t.Helper()
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": tool, "input": input, "tool_use_id": "tool-" + id,
		"agent_id": "agent-context", "permission_suggestions": []any{map[string]any{"future_rule": true}},
		"blocked_path": "outside/project", "decision_reason": "host approval", "title": "Approval question",
		"display_name": "Tool action", "description": "Review input", "future_context": map[string]any{"kept": 7},
	}})
	event := receive(t, c)
	require.NotNilf(t, event.Control, "expected permission %q, got %s", id, event.Raw)
	require.EqualValuesf(t, id, event.Control.RequestID, "expected permission %q, got %s", id, event.Raw)
	return event
}

func awaitPermission(t *testing.T, requests <-chan *PermissionRequest) *PermissionRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		require.FailNow(t, "permission handler was not dispatched")
		return nil
	}
}

func awaitPermissionError(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(3 * time.Second):
		require.FailNow(t, "permission answer did not finish")
		return nil
	}
}

func permissionSnapshot(t *testing.T, c *Client) []json.RawMessage {
	t.Helper()
	send(t, c, "snapshot")
	event := receive(t, c)
	require.EqualValuesf(t, "snapshot", event.Type, "unexpected unsolicited output before snapshot: %s", event.Raw)
	var snapshot struct {
		Frames []json.RawMessage `json:"frames"`
	}
	{
		err := json.Unmarshal(event.Raw, &snapshot)
		require.NoError(t, err)
	}
	return snapshot.Frames
}

func permissionResponses(t *testing.T, frames []json.RawMessage) []map[string]any {
	t.Helper()
	var responses []map[string]any
	for _, frame := range frames {
		var wire map[string]any
		{
			err := json.Unmarshal(frame, &wire)
			require.NoError(t, err)
		}
		if wire["type"] == "control_response" {
			responses = append(responses, wire)
		}
		if wire["type"] == "user" {
			message := wire["message"].(map[string]any)
			{
				_, ok := message["content"].(string)
				require.Truef(t, ok, "invented tool result: %s", frame)
			}
		}
	}
	return responses
}

func assertPermissionReply(t *testing.T, c *Client, id string, decision map[string]any) {
	t.Helper()
	event := receive(t, c)
	var echoed struct {
		Wire map[string]any `json:"wire"`
	}
	require.EqualValuesf(t, "reply_seen", event.Type, "expected response echo: %s", event.Raw)
	require.Nilf(t, json.Unmarshal(event.Raw, &echoed), "expected response echo: %s", event.Raw)
	want := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": decision}}
	require.Equalf(t, echoed.Wire, want, "response mismatch:\ngot  %#v\nwant %#v", echoed.Wire, want)
}

func permissionTranscript(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var frames []json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			frames = append(frames, json.RawMessage(line))
		}
	}
	return frames
}

func (s *controlInteractionSuite) TestDeferredPermissionHumanWaitKeepsTransportMoving() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	answers := make(chan error, 2)
	releaseHuman := make(chan struct{})
	handlerReturned := make(chan struct{}, 2)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.PermissionWorkers = 2
		opts.PermissionHandler = func(p *PermissionRequest) {
			defer func() { handlerReturned <- struct{}{} }()
			requests <- p
			if p.RequestID() == "human" {
				select {
				case <-releaseHuman:
				case <-p.Context().Done():
					return
				}
				answers <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
			} else {
				answers <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "Not approved\n理由"})
			}
		}
	})
	event := emitPermission(t, c, "human", "Read", json.RawMessage(`{"path":"review.txt","unknown":[1,true]}`))
	human := awaitPermission(t, requests)
	require.EqualValues(t, "human", human.RequestID(), "permission identifiers lost")
	require.EqualValues(t, "tool-human", human.ToolUseID(), "permission identifiers lost")
	require.EqualValues(t, "Read", human.ToolName(), "permission identifiers lost")
	require.NotEqualValues(t, 0, human.Epoch(), "permission identifiers lost")
	require.Nil(t, human.Context().Err(), "fresh handle already cancelled")
	var raw map[string]any
	{
		err := json.Unmarshal(human.Raw(), &raw)
		require.NoError(t, err)
	}
	require.EqualValuesf(t, "agent-context", raw["agent_id"], "context lost: %s", human.Raw())
	require.EqualValuesf(t, "host approval", raw["decision_reason"], "context lost: %s", human.Raw())
	require.EqualValuesf(t, float64(7), raw["future_context"].(map[string]any)["kept"], "context lost: %s", human.Raw())
	// Returned bytes and public event metadata cannot rewrite the authority or
	// original input used by a later allow.
	for _, data := range [][]byte{human.Input(), human.Raw(), event.Control.Request} {
		for i := range data {
			data[i] = ' '
		}
	}
	event.Control.RequestID = "tampered"
	emitPermissionFrame(t, c, map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "still moving"}}})
	require.NotNil(t, receive(t, c).Stream, "human wait blocked stream event")
	emitPermission(t, c, "independent", "Write", json.RawMessage(`{"path":"other.txt"}`))
	second := awaitPermission(t, requests)
	require.EqualValues(t, "independent", second.RequestID(), "second permission was not independent")
	require.EqualValues(t, human.Epoch(), second.Epoch(), "second permission was not independent")
	{
		err := awaitPermissionError(t, answers)
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "independent", map[string]any{"behavior": "deny", "message": "Not approved\n理由"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	{
		err := c.Interrupt(ctx)
		require.NoErrorf(t, err, "human wait blocked control: %v", err)
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "early response: %#v", got)
	}
	close(releaseHuman)
	{
		err := awaitPermissionError(t, answers)
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "human", map[string]any{"behavior": "allow", "updatedInput": map[string]any{"path": "review.txt", "unknown": []any{float64(1), true}}})
	for range 2 {
		select {
		case <-handlerReturned:
		case <-time.After(3 * time.Second):
			require.FailNow(t, "handler did not return")
		}
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 2, len(got), "duplicate handler-return response: %#v", got)
	}
}

func (s *controlInteractionSuite) TestDeferredPermissionRetainedHandleUsesExactUpdatedInput() {
	t := s.T()
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
			requests := make(chan *PermissionRequest, 1)
			returned := make(chan struct{})
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.PermissionHandler = func(p *PermissionRequest) { requests <- p; close(returned) }
			})
			emitPermission(t, c, "original/request-ID", tc.tool, json.RawMessage(`{"original":true}`))
			p := awaitPermission(t, requests)
			<-returned
			{
				got := permissionResponses(t, permissionSnapshot(t, c))
				require.EqualValuesf(t, 0, len(got), "handler return answered: %#v", got)
			}
			{
				err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: tc.updated})
				require.NoError(t, err)
			}
			assertPermissionReply(t, c, "original/request-ID", map[string]any{"behavior": "allow", "updatedInput": tc.want})
			copyOfHandle := *p
			{
				err := copyOfHandle.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
				require.ErrorIsf(t, err, ErrPermissionUsed, "copy reused authority: %v", err)
			}
			require.ErrorIs(t, context.Cause(p.Context()), ErrPermissionUsed, "completed handle context remained live")
			{
				got := permissionResponses(t, permissionSnapshot(t, c))
				require.EqualValuesf(t, 1, len(got), "duplicate response: %#v", got)
			}
		})
	}
}

func (s *controlInteractionSuite) TestAssistantQuestionIsOnlyAnEvent() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.PermissionWorkers = 1
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermissionFrame(t, c, map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "question-tool", "name": "AskUserQuestion", "input": map[string]any{"questions": []any{}}}}}})
	event := receive(t, c)
	require.NotNilf(t, event.Assistant, "question event lost: %s", event.Raw)
	require.EqualValuesf(t, "AskUserQuestion", event.Assistant.Content.Blocks[0].ToolUse.Name, "question event lost: %s", event.Raw)
	// One worker and a later real request form a dispatch-order barrier. A
	// mistakenly enqueued assistant handle would arrive before this real one.
	emitPermission(t, c, "real-control", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	require.EqualValues(t, "real-control", p.RequestID(), "assistant block created an answer authority")
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "real-control", map[string]any{"behavior": "deny", "message": ""})
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "assistant block caused response: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionCancellationImmediatelyInvalidatesHandle() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "abandoned", "AskUserQuestion", json.RawMessage(`{"questions":[]}`))
	p := awaitPermission(t, requests)
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "abandoned", "future": "preserved"})
	cancelEvent := receive(t, c)
	require.EqualValuesf(t, "control_cancel_request", cancelEvent.Type, "cancel frame lost: %s", cancelEvent.Raw)
	require.Containsf(t, string(cancelEvent.Raw), `"future":"preserved"`, "cancel frame lost: %s", cancelEvent.Raw)
	require.ErrorIs(t, context.Cause(p.Context()), ErrPermissionCancelled, "observed cancel did not invalidate context immediately")
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIsf(t, err, ErrPermissionCancelled, "canceled answer: %v", err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "unknown"})
	require.EqualValues(t, "control_cancel_request", receive(t, c).Type, "unknown cancellation was hidden")
	emitPermission(t, c, "still-live", "Read", json.RawMessage(`{}`))
	other := awaitPermission(t, requests)
	{
		err := other.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "no"})
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "still-live", map[string]any{"behavior": "deny", "message": "no"})
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "cancellation produced response: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionInvalidAnswerAndPanicFailClosed() {
	t := s.T()
	cases := []struct {
		name   string
		answer PermissionAnswer
		panic  bool
	}{
		{name: "panic", panic: true},
		{name: "nil panic", panic: true},
		{name: "missing behavior"},
		{name: "unknown behavior", answer: PermissionAnswer{Behavior: "ask"}},
		{name: "invalid JSON", answer: PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: json.RawMessage(`{`)}},
		{name: "null", answer: PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: json.RawMessage(`null`)}},
		{name: "array", answer: PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: json.RawMessage(`[]`)}},
		{name: "empty bytes", answer: PermissionAnswer{Behavior: PermissionAllow, UpdatedInput: json.RawMessage{}}},
		{name: "allow reason", answer: PermissionAnswer{Behavior: PermissionAllow, Message: "contradictory"}},
		{name: "deny input", answer: PermissionAnswer{Behavior: PermissionDeny, UpdatedInput: json.RawMessage(`{}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "nil panic" {
				t.Setenv("GODEBUG", "panicnil=1")
			}
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			results := make(chan error, 1)
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *PermissionRequest) {
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
			want := ErrInvalidPermissionAnswer
			if tc.panic {
				want = ErrPermissionHandlerPanic
			} else if err := awaitPermissionError(t, results); !errors.Is(err, want) {
				require.FailNowf(t, "unexpected test outcome", "invalid answer error: %v", err)
			}
			awaitDone(t, c)
			{
				err := c.Close()
				require.ErrorIsf(t, err, want, "failure was not observable: %v", err)
			}
			var failure *PermissionError
			require.ErrorAsf(t, c.Err(), &failure, "missing request-specific failure: %v", c.Err())
			require.EqualValuesf(t, "bad-handler", failure.RequestID, "missing request-specific failure: %v", c.Err())
			{
				got := permissionResponses(t, permissionTranscript(t, path))
				require.EqualValuesf(t, 0, len(got), "failed handler granted/responded: %#v", got)
			}
		})
	}
}

func (s *controlInteractionSuite) TestPermissionAnswerConcurrentSingleUse() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
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
			results <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: fmt.Sprint(i)})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrPermissionUsed) {
			require.FailNowf(t, "unexpected test outcome", "concurrent answer: %v", err)
		}
	}
	require.EqualValuesf(t, 1, successes, "successful responses=%d", successes)
	require.EqualValues(t, "reply_seen", receive(t, c).Type, "response missing")
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "duplicate responses: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionCloseInvalidatesAndFreshClientHasNewEpoch() {
	t := s.T()
	requests := make(chan *PermissionRequest, 2)
	configure := func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	}
	first := s.openFixture(t, "permission_controls", configure)
	emitPermission(t, first, "reused-id", "Read", json.RawMessage(`{}`))
	old := awaitPermission(t, requests)
	{
		err := first.Close()
		require.NoError(t, err)
	}
	require.ErrorIs(t, context.Cause(old.Context()), ErrClosed, "Close did not cancel pending context")
	second := s.openFixture(t, "permission_controls", configure)
	emitPermission(t, second, "reused-id", "Read", json.RawMessage(`{}`))
	fresh := awaitPermission(t, requests)
	require.NotEqualValues(t, fresh.Epoch(), old.Epoch(), "connection epoch reused")
	{
		err := old.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIsf(t, err, ErrClosed, "old answer: %v", err)
	}
	{
		err := fresh.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.NoError(t, err)
	}
	assertPermissionReply(t, second, "reused-id", map[string]any{"behavior": "deny", "message": ""})
	{
		got := permissionResponses(t, permissionSnapshot(t, second))
		require.EqualValuesf(t, 1, len(got), "stale response crossed client: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionWorkerBoundAndCloseDoNotWaitForUserCode() {
	t := s.T()
	requests := make(chan *PermissionRequest, 3)
	release := make(chan struct{})
	returned := make(chan struct{})
	defer close(release)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.PermissionWorkers = 1
		opts.Limits.ControlBuffer = 1
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p; <-release; close(returned) }
	})
	emitPermission(t, c, "running", "Read", json.RawMessage(`{}`))
	running := awaitPermission(t, requests)
	emitPermission(t, c, "queued", "Read", json.RawMessage(`{}`))
	require.EqualValues(t, 0, len(requests), "worker limit exceeded")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	{
		err := c.Interrupt(ctx)
		require.NoError(t, err)
	}
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "overflow", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
	awaitDone(t, c)
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	{
		err := awaitPermissionError(t, closed)
		require.ErrorIsf(t, err, ErrResourceLimit, "overflow: %v", err)
	}
	require.NotNil(t, running.Context().Err(), "overflow left running permission context live")
	select {
	case <-returned:
		require.FailNow(t, "test handler unexpectedly returned before release")
	default:
	}
}

func (s *controlInteractionSuite) TestPermissionDuplicateIDCannotCreateSecondAuthority() {
	t := s.T()
	for _, subtype := range []string{"can_use_tool", "unsupported"} {
		t.Run(subtype, func(t *testing.T) {
			requests := make(chan *PermissionRequest, 2)
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "duplicate", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "duplicate", "request": map[string]any{"subtype": subtype, "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			{
				err := c.Close()
				require.ErrorIsf(t, err, ErrProtocol, "duplicate ID not rejected: %v", err)
			}
			{
				err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
				require.ErrorIsf(t, err, ErrClosed, "duplicate invalidation: %v", err)
			}
			{
				got := permissionResponses(t, permissionTranscript(t, path))
				require.EqualValuesf(t, 0, len(got), "duplicate created responder: %#v", got)
			}
		})
	}
}

func (s *controlInteractionSuite) TestPermissionLateValidControlDoesNotMakeCloseProtocolError() {
	t := s.T()
	c := s.openFixture(t, "permission_controls")
	send(t, c, "late_control_on_close")
	require.EqualValues(t, "ready", receive(t, c).Type, "fixture not ready")
	{
		err := c.Close()
		require.NoErrorf(t, err, "valid shutdown frame became error: %v", err)
	}
	{
		event := receive(t, c)
		require.NotNilf(t, event.Control, "late control was not retained: %s", event.Raw)
		require.EqualValuesf(t, "late", event.Control.RequestID, "late control was not retained: %s", event.Raw)
	}
	{
		_, err := nextNative(c, context.Background())
		require.ErrorIsf(t, err, io.EOF, "shutdown stream end: %v", err)
	}
}

func (s *controlInteractionSuite) TestPermissionCanceledCallerContextDoesNotConsumeHandle() {
	t := s.T()
	requests := make(chan *PermissionRequest, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
	})
	emitPermission(t, c, "retry-before-reserve", "Read", json.RawMessage(`{}`))
	p := awaitPermission(t, requests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := p.Answer(ctx, PermissionAnswer{Behavior: PermissionAllow})
		require.ErrorIsf(t, err, context.Canceled, "already canceled answer: %v", err)
		require.NotErrorIsf(t, err, ErrUnknownOutcome, "already canceled answer: %v", err)
	}
	{
		err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
		require.NoError(t, err)
	}
	assertPermissionReply(t, c, "retry-before-reserve", map[string]any{"behavior": "deny", "message": ""})
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 1, len(got), "canceled call wrote: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionQueuedCancellationNeverInvokesHandler() {
	t := s.T()
	requests := make(chan *PermissionRequest, 3)
	release := make(chan struct{})
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Limits.PermissionWorkers = 1
		opts.PermissionHandler = func(p *PermissionRequest) {
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
	require.EqualValues(t, "control_cancel_request", receive(t, c).Type, "cancel missing")
	// The independent automatic responder remains available while every user
	// worker is occupied.
	emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "unsupported", "request": map[string]any{"subtype": "future_control"}})
	require.NotNil(t, receive(t, c).Control, "unsupported event missing")
	echo := receive(t, c)
	var reply struct {
		Wire map[string]any `json:"wire"`
	}
	require.EqualValuesf(t, "reply_seen", echo.Type, "independent unsupported response: %s", echo.Raw)
	require.Nilf(t, json.Unmarshal(echo.Raw, &reply), "independent unsupported response: %s", echo.Raw)
	require.Equalf(t, reply.Wire, map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": "unsupported", "error": "Unsupported control request subtype: future_control"}}, "independent unsupported response: %s", echo.Raw)
	close(release)
	emitPermission(t, c, "last", "Read", json.RawMessage(`{}`))
	last := awaitPermission(t, requests)
	require.EqualValuesf(t, "last", last.RequestID(), "canceled queued handler invoked: %s", last.RequestID())
	for _, p := range []*PermissionRequest{first, last} {
		{
			err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
			require.NoError(t, err)
		}
		assertPermissionReply(t, c, p.RequestID(), map[string]any{"behavior": "deny", "message": ""})
	}
	{
		got := permissionResponses(t, permissionSnapshot(t, c))
		require.EqualValuesf(t, 3, len(got), "queued cancel answered: %#v", got)
	}
}

func (s *controlInteractionSuite) TestPermissionPanicAfterAnswerDoesNotCreateSecondResponse() {
	t := s.T()
	path := filepath.Join(t.TempDir(), "transcript.ndjson")
	results := make(chan error, 1)
	c := s.openFixture(t, "permission_controls", func(opts *Options) {
		opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
		opts.PermissionHandler = func(p *PermissionRequest) {
			results <- p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny, Message: "explicit"})
			panic("after response")
		}
	})
	emitPermission(t, c, "answered-before-panic", "Read", json.RawMessage(`{}`))
	{
		err := awaitPermissionError(t, results)
		require.NoError(t, err)
	}
	awaitDone(t, c)
	{
		err := c.Close()
		require.ErrorIs(t, err, ErrPermissionHandlerPanic, err)
	}
	{
		got := permissionResponses(t, permissionTranscript(t, path))
		require.EqualValuesf(t, 1, len(got), "panic duplicated response: %#v", got)
	}
}

func TestPermissionZeroHandleCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request *PermissionRequest
	}{
		{name: "nil"}, {name: "zero", request: new(PermissionRequest)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Answer(context.Background(), PermissionAnswer{Behavior: PermissionAllow})
			require.ErrorIs(t, err, ErrInvalidPermissionRequest, "zero handle")
			require.ErrorIs(t, context.Cause(tc.request.Context()), ErrInvalidPermissionRequest, "zero context live")
		})
	}
}

func (s *controlInteractionSuite) TestIncomingControlHistoryRejectsRetiredIDReuse() {
	t := s.T()
	for _, previous := range []string{"unsupported", "answered", "cancelled", "unknown cancellation"} {
		t.Run(previous, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transcript.ndjson")
			requests := make(chan *PermissionRequest, 2)
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				opts.Env = append(opts.Env, "CLAUDE_GO_TRANSCRIPT="+path)
				opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
			})
			wantResponses := 0
			switch previous {
			case "unsupported":
				emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "retired", "request": map[string]any{"subtype": "unsupported"}})
				require.NotNil(t, receive(t, c).Control, "unsupported request did not finish")
				require.EqualValues(t, "reply_seen", receive(t, c).Type, "unsupported request did not finish")
				wantResponses = 1
			case "answered", "cancelled":
				emitPermission(t, c, "retired", "Read", json.RawMessage(`{}`))
				p := awaitPermission(t, requests)
				if previous == "answered" {
					{
						err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
						require.NoError(t, err)
					}
					assertPermissionReply(t, c, "retired", map[string]any{"behavior": "deny", "message": ""})
					wantResponses = 1
				} else {
					emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "retired"})
					require.EqualValues(t, "control_cancel_request", receive(t, c).Type, "cancel missing")
				}
			case "unknown cancellation":
				emitPermissionFrame(t, c, map[string]any{"type": "control_cancel_request", "request_id": "retired"})
				require.EqualValues(t, "control_cancel_request", receive(t, c).Type, "cancel missing")
			}
			subtype := "can_use_tool"
			if previous == "answered" || previous == "cancelled" {
				subtype = "unsupported"
			}
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "retired", "request": map[string]any{"subtype": subtype, "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			{
				err := c.Close()
				require.ErrorIsf(t, err, ErrProtocol, "retired ID revived: %v", err)
			}
			{
				got := permissionResponses(t, permissionTranscript(t, path))
				require.EqualValuesf(t, wantResponses, len(got), "retired ID responded again: %#v", got)
			}
		})
	}
}

func (s *controlInteractionSuite) TestPermissionRegistryBoundsFailClosed() {
	t := s.T()
	for _, bound := range []string{"pending", "history"} {
		t.Run(bound, func(t *testing.T) {
			requests := make(chan *PermissionRequest, 2)
			c := s.openFixture(t, "permission_controls", func(opts *Options) {
				if bound == "pending" {
					opts.Limits.PendingPermissions = 1
				} else {
					opts.Limits.PermissionHistory = 1
				}
				opts.PermissionHandler = func(p *PermissionRequest) { requests <- p }
			})
			emitPermission(t, c, "first", "Read", json.RawMessage(`{}`))
			p := awaitPermission(t, requests)
			if bound == "history" {
				{
					err := p.Answer(context.Background(), PermissionAnswer{Behavior: PermissionDeny})
					require.NoError(t, err)
				}
				assertPermissionReply(t, c, "first", map[string]any{"behavior": "deny", "message": ""})
			}
			emitPermissionFrame(t, c, map[string]any{"type": "control_request", "request_id": "excess", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Read", "input": map[string]any{}}})
			awaitDone(t, c)
			{
				err := c.Close()
				require.ErrorIsf(t, err, ErrResourceLimit, "registry overflow: %v", err)
			}
			require.NotNil(t, p.Context().Err(), "overflow left pending context live")
		})
	}
}
