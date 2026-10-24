// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

func sessionPermission(t *testing.T, s *Session, id, tool, input string) *IncomingRequest {
	t.Helper()
	submitFrame(t, s, `{"type":"control_request","request_id":"`+id+`","future":null,"request":{"subtype":"can_use_tool","tool_name":"`+tool+`","input":`+input+`,"agent_id":"unverified"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		r, err := s.Receive(ctx)
		require.NoError(t, err)
		if r.Kind == ReceivedRequest {
			require.Nil(t, r.Event)
			require.Equal(t, id, r.Request.ID())
			require.JSONEq(t, string(r.Raw), string(r.Request.Raw()))
			require.JSONEq(t, string(r.Params), string(r.Request.Params()))
			return r.Request
		}
	}
}

func TestSessionPermissionExtensionsAndQuestions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	s := openSessionFixture(t, func(o *SessionOptions) { o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path) })
	p := sessionPermission(t, s, "extended", "Read", `{}`)
	require.NoError(t, p.Approve(context.Background(), ApprovalOptions{UpdatedPermissions: []types.PermissionUpdate{{Type: "setMode", Mode: "acceptEdits", Destination: "session"}}}))
	d := sessionPermission(t, s, "deny", "Read", `{}`)
	interrupt := true
	require.NoError(t, d.Deny(context.Background(), DenialOptions{Message: "declined", Interrupt: &interrupt}))
	q := sessionPermission(t, s, "question", "AskUserQuestion", `{"questions":[{"question":"Which complete format?","header":"Format","options":[{"label":"Blue","description":"Use blue","preview":"sample","future":null}],"multiSelect":false,"future":42}],"metadata":{"x":null}}`)
	require.NoError(t, q.Answer(context.Background(), QuestionAnswers{Answers: map[string]string{"Which complete format?": "Blue"}}))
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, s.Close())
	responses := permissionResponses(t, permissionTranscript(t, path))
	require.Len(t, responses, 3)
	want := []string{
		`{"behavior":"allow","updatedInput":{},"updatedPermissions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}]}`,
		`{"behavior":"deny","message":"declined","interrupt":true}`,
		`{"behavior":"allow","updatedInput":{"questions":[{"question":"Which complete format?","header":"Format","options":[{"label":"Blue","description":"Use blue","preview":"sample","future":null}],"multiSelect":false,"future":42}],"metadata":{"x":null},"answers":{"Which complete format?":"Blue"}}}`,
	}
	for i, response := range responses {
		data, err := json.Marshal(response["response"].(map[string]any)["response"])
		require.NoError(t, err)
		require.JSONEq(t, want[i], string(data))
	}
}

func TestSessionPermissionCancellationCloseAndSnapshot(t *testing.T) {
	s := openSessionFixture(t)
	p := sessionPermission(t, s, "cancel", "Read", `{"file_path":"original","future":null}`)
	raw, params, input := p.Raw(), p.Params(), p.Handle().Input()
	// Modifying detached snapshots cannot retarget the original handle.
	mutated := p.Raw()
	mutated[0] = '['
	submitFrame(t, s, `{"type":"control_cancel_request","request_id":"cancel"}`)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.ErrorIs(t, p.Approve(context.Background(), ApprovalOptions{}), ErrPermissionCancelled)
	pending := sessionPermission(t, s, "pending", "Read", `{}`)
	require.NoError(t, s.Close())
	require.ErrorIs(t, pending.Deny(context.Background(), DenialOptions{}), ErrClosed)
	require.Equal(t, raw, p.Raw())
	require.Equal(t, params, p.Params())
	require.Equal(t, input, p.Handle().Input())
	for {
		_, err := s.Receive(context.Background())
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
	}
}

func TestSessionPermissionConcurrentAnswersAndClose(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "answers", true: "close"}[closeFirst], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wire.jsonl")
			s := openSessionFixture(t, func(o *SessionOptions) { o.Env = append(o.Env, "CLAUDE_GO_TRANSCRIPT="+path) })
			p := sessionPermission(t, s, "race", "Read", `{}`)
			start := make(chan struct{})
			results := make(chan error, 16)
			var workers sync.WaitGroup
			for range 16 {
				workers.Go(func() { <-start; results <- p.Deny(context.Background(), DenialOptions{Message: "declined"}) })
			}
			var closed chan error
			if closeFirst {
				closed = make(chan error, 1)
				go func() { <-start; closed <- s.Close() }()
			}
			close(start)
			workers.Wait()
			close(results)
			succeeded := 0
			for err := range results {
				if err == nil {
					succeeded++
				} else {
					require.True(t, errors.Is(err, ErrPermissionUsed) || errors.Is(err, ErrClosed) || errors.Is(err, ErrUnknownOutcome), "%v", err)
				}
			}
			if closed != nil {
				require.NoError(t, <-closed)
			} else {
				require.NoError(t, s.Close())
				require.Equal(t, 1, succeeded)
			}
			require.LessOrEqual(t, succeeded, 1)
			require.LessOrEqual(t, len(permissionResponses(t, permissionTranscript(t, path))), 1)
			require.Error(t, p.Context().Err())
		})
	}
}

func TestSessionPermissionHistoryLimitsAndDuplicateNeverRegrant(t *testing.T) {
	for _, kind := range []string{"duplicate", "history", "pending", "cancel-before-request"} {
		t.Run(kind, func(t *testing.T) {
			s := openSessionFixture(t, func(o *SessionOptions) { o.Limits.PermissionHistory = 2; o.Limits.PendingPermissions = 1 })
			p := sessionPermission(t, s, "old", "Read", `{}`)
			if kind != "pending" {
				require.NoError(t, p.Deny(context.Background(), DenialOptions{}))
			}
			id := "old"
			if kind == "history" {
				other := sessionPermission(t, s, "other", "Read", `{}`)
				require.NoError(t, other.Deny(context.Background(), DenialOptions{}))
				id = "overflow"
			}
			if kind == "pending" {
				id = "overflow"
			}
			if kind == "cancel-before-request" {
				id = "cancelled"
				submitFrame(t, s, `{"type":"control_cancel_request","request_id":"cancelled"}`)
			}
			submitFrame(t, s, `{"type":"control_request","request_id":"`+id+`","request":{"subtype":"can_use_tool","tool_name":"Read","input":{}}}`)
			select {
			case <-s.Done():
			case <-time.After(time.Second):
				t.Fatal("identity/resource failure waited for Receive")
			}
			if kind == "history" || kind == "pending" {
				require.ErrorIs(t, s.Err(), ErrResourceLimit)
			} else {
				require.ErrorIs(t, s.Err(), ErrProtocol)
			}
			require.Error(t, p.Approve(context.Background(), ApprovalOptions{}))
			for {
				r, err := s.Receive(context.Background())
				if err != nil {
					require.Error(t, err)
					break
				}
				require.Nil(t, r.Request, "a second authority was delivered")
			}
		})
	}
}

func TestSessionPermissionWaitsWhileReaderContinues(t *testing.T) {
	s := openSessionFixture(t)
	p := sessionPermission(t, s, "approval", "Read", `{"file_path":"original"}`)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	submitFrame(t, s, `{"type":"result","session_id":"s","is_error":true,"result":"native failure"}`)
	require.Equal(t, "native failure", sessionNative(t, s).Event.Result.Result)
	require.NoError(t, s.Interrupt(context.Background(), InterruptOptions{}))
	require.NoError(t, p.Context().Err())
	require.NoError(t, s.Err())
	require.NoError(t, p.Approve(context.Background(), ApprovalOptions{UpdatedInput: json.RawMessage(`{"file_path":"changed"}`)}))
	require.ErrorIs(t, p.Deny(context.Background(), DenialOptions{}), ErrPermissionUsed)
}

func TestSessionPermissionNotSentKeepsOriginalHandle(t *testing.T) {
	s := openSessionFixture(t)
	p := sessionPermission(t, s, "local-rejection", "Read", `{}`)
	err := p.Approve(context.Background(), ApprovalOptions{ToolUseID: "override"})
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	require.Equal(t, NotSent, dispatch.State)
	require.ErrorIs(t, err, ErrUnsupported)
	require.NoError(t, p.Context().Err())
	require.Error(t, p.Approve(nil, ApprovalOptions{}))
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	require.Equal(t, "init", sessionNative(t, s).Event.Subtype)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = p.Approve(ctx, ApprovalOptions{})
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, p.Deny(context.Background(), DenialOptions{Message: "later"}))
}

func TestSessionQuestionRejectsUnverifiedAnswersWithoutConsumingHandle(t *testing.T) {
	s := openSessionFixture(t)
	p := sessionPermission(t, s, "question", "AskUserQuestion", `{"questions":[{"question":"Full text?","options":[{"label":"A"}],"multiSelect":true}]}`)
	for _, input := range []string{
		`{"questions":[{"question":"Full text?","options":[],"multiSelect":true}],"answers":{"Full text?":["A"]}}`,
		`{"questions":[{"question":"Full text?","options":[]}],"response":"output-only"}`,
	} {
		err := p.Approve(context.Background(), ApprovalOptions{UpdatedInput: json.RawMessage(input)})
		var dispatch *DispatchError
		require.ErrorAs(t, err, &dispatch)
		require.Equal(t, NotSent, dispatch.State)
		require.ErrorIs(t, err, ErrUnsupported)
	}
	err := p.Answer(context.Background(), QuestionAnswers{Answers: map[string]string{"header": "A"}})
	require.ErrorIs(t, err, ErrInvalidPermissionAnswer)
	var dispatch *DispatchError
	require.ErrorAs(t, err, &dispatch)
	require.Equal(t, NotSent, dispatch.State)
	require.NoError(t, p.Context().Err())
	require.NoError(t, p.Answer(context.Background(), QuestionAnswers{Answers: map[string]string{"Full text?": "A"}}))
}

func TestSessionRejectsUnprivilegedQuestions(t *testing.T) {
	s := openSessionFixture(t)
	submitFrame(t, s, `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.258"}`)
	sessionNative(t, s)
	frame := `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","name":"AskUserQuestion","id":"ordinary","input":{"questions":[]}}]}}`
	submitFrame(t, s, frame)
	r := sessionNative(t, s)
	require.Nil(t, r.Request)
	parsed, err := ParseEvent([]byte(frame))
	require.NoError(t, err)
	require.Nil(t, parsed.Control)
	var zero IncomingRequest
	require.Error(t, zero.Answer(context.Background(), QuestionAnswers{Answers: map[string]string{}}))
	p := sessionPermission(t, s, "read", "Read", `{}`)
	require.ErrorIs(t, p.Answer(context.Background(), QuestionAnswers{Answers: map[string]string{}}), ErrUnsupported)
	require.NoError(t, p.Deny(context.Background(), DenialOptions{}))
}
