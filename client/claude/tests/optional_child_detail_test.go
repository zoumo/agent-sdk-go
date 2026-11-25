package claude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/zoumo/agent-sdk-go/client/claude"
	clt "github.com/zoumo/agent-sdk-go/client/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
)

type claudeChildDetailSuite struct{ suite.Suite }

func TestOptionalChildDetail(t *testing.T) { suite.Run(t, new(claudeChildDetailSuite)) }

// Only echoes authored synthetic frames and records local protocol input.
func claudeChildDetailPeer() error {
	trace, err := os.Create(os.Getenv("SDK_INTEGRATION_TRACE"))
	if err != nil {
		return err
	}
	defer trace.Close()
	args, err := json.Marshal(os.Args[1:])
	if err != nil {
		return err
	}
	fmt.Fprintln(trace, string(args))
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		fmt.Fprintln(trace, string(raw))
		var f struct {
			Type, RequestID string
			Request         struct{ Subtype string }
			Message         struct{ Content string }
			Response        json.RawMessage
		}
		// request_id uses its native spelling, unlike the local fixture instruction.
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		_ = json.Unmarshal(wire["request_id"], &f.RequestID)
		switch f.Type {
		case "control_request":
			fmt.Fprintf(os.Stdout, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`+"\n", f.RequestID)
		case "user":
			fmt.Fprintln(os.Stdout, f.Message.Content)
		}
	}
	return scanner.Err()
}

func (s *claudeChildDetailSuite) TestDeliveryPolicy() {
	s.Run("F1-default-options", func() { checkClaudeChildDetailOptions(s) })
	s.Run("F2-pure-detail", func() { checkClaudePureDetail(s) })
	s.Run("F3-unknown-and-mixed", func() { checkClaudeMixedDetail(s) })
	s.Run("F4-authority", func() { checkClaudeDetailAuthority(s) })
	s.Run("F5-lifecycle", func() { checkClaudeDetailLifecycle(s) })
}

const (
	claudeDetailInit = `{"type":"system","subtype":"init","session_id":"s","claude_code_version":"2.1.293"}`
	claudeDetailTask = `{"type":"system","subtype":"task_started","session_id":"s","task_id":"a","task_type":"local_agent","tool_use_id":"spawn"}`
	claudeDetailText = ` {"type":"assistant","session_id":"s","parent_tool_use_id":"spawn","message":{"content":[{"type":"text","text":"child"}]}} `
)

func openClaudeDetail(s *claudeChildDetailSuite, policy claude.ChildDetailPolicy, resume string) (*claude.Session, string) {
	t := s.T()
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace.jsonl")
	o := claude.SessionOptions{CLIPath: exe, WorkDir: dir, ResumeID: resume, ChildDetailPolicy: policy, Env: []string{"HOME=" + dir, "SDK_INTEGRATION_PEER=claude", "SDK_INTEGRATION=child-detail", "SDK_INTEGRATION_TRACE=" + trace, "GORACE=atexit_sleep_ms=0"}}
	session, err := claude.Open(testsupport.Deadline(t), o)
	require.NoError(t, err)
	o.ChildDetailPolicy = claude.ChildDetailSuppress // construction fixes a value copy
	t.Cleanup(func() { _ = session.Abort() })
	return session, trace
}

func sendClaudeDetail(s *claudeChildDetailSuite, session *claude.Session, frames ...string) {
	t := s.T()
	t.Helper()
	for _, frame := range frames {
		receipt, err := session.Submit(testsupport.Deadline(t), claude.Input{Text: frame})
		require.NoError(t, err)
		require.True(t, receipt.Written)
	}
	require.NoError(t, session.Interrupt(testsupport.Deadline(t), claude.InterruptOptions{}))
}

func drainClaudeDetail(s *claudeChildDetailSuite, session *claude.Session) []claude.Received {
	t := s.T()
	t.Helper()
	require.NoError(t, session.Close())
	var result []claude.Received
	for {
		r, err := session.Receive(testsupport.Deadline(t))
		if err != nil {
			require.ErrorIs(t, err, io.EOF)
			break
		}
		if r.Event != nil {
			require.Equal(t, r.Raw, r.Event.Raw)
			result = append(result, r)
		}
	}
	return result
}

func checkClaudeChildDetailOptions(s *claudeChildDetailSuite) {
	t := s.T()
	for _, policy := range []claude.ChildDetailPolicy{"", "preserve", "suppress"} {
		for _, resume := range []string{"", "explicit-resume"} {
			session, trace := openClaudeDetail(s, policy, resume)
			sendClaudeDetail(s, session, claudeDetailInit, claudeDetailTask, claudeDetailText)
			got := drainClaudeDetail(s, session)
			expected := 3
			if policy == claude.ChildDetailSuppress {
				expected = 2
			}
			require.Len(t, got, expected)
			if expected == 3 {
				require.Equal(t, claudeDetailText, string(got[2].Raw)) //nolint:testifylint // preserves exact protocol bytes
			}
			raw, err := os.ReadFile(trace)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var args []string
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &args))
			if resume != "" {
				require.Contains(t, args, "--resume="+resume)
				require.Equal(t, resume, session.RequestedResumeID())
			}
			var init struct{ Request map[string]any }
			require.NoError(t, json.Unmarshal([]byte(lines[1]), &init))
			require.Equal(t, true, init.Request["forwardSubagentText"])
			require.NotContains(t, init.Request, "child_detail_policy")
		}
	}
	for _, policy := range []claude.ChildDetailPolicy{"SUPPRESS", "unknown", " preserve"} {
		session, err := claude.Open(context.Background(), claude.SessionOptions{CLIPath: "/nonexistent-require.NoError-not-start", ChildDetailPolicy: policy})
		require.Nil(t, session)
		require.ErrorIs(t, err, claude.ErrInvalidInput)
	}
}

func checkClaudePureDetail(s *claudeChildDetailSuite) {
	t := s.T()
	details := []string{
		claudeDetailText,
		`{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn","message":{"content":[{"type":"thinking","thinking":"reason","signature":"sig"},{"type":"redacted_thinking","data":"opaque"}],"usage":{"input_tokens":2,"output_tokens":7},"stop_reason":"end_turn"}}`,
	}
	for _, delta := range []string{`{"type":"text_delta","text":"next"}`, `{"type":"thinking_delta","thinking":"reason"}`, `{"type":"signature_delta","signature":"sig"}`, `{"type":"input_json_delta","partial_json":"{\"x\":"}`} {
		details = append(details, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":{"type":"content_block_delta","index":0,"delta":`+delta+`}}`)
	}
	for _, policy := range []claude.ChildDetailPolicy{"", claude.ChildDetailPreserve, claude.ChildDetailSuppress} {
		session, _ := openClaudeDetail(s, policy, "")
		frames := []string{claudeDetailInit, claudeDetailTask}
		want := append([]string(nil), frames...)
		for _, detail := range details {
			frames = append(frames, detail, detail) // repeated snapshots/deltas are never deduplicated
			if policy != claude.ChildDetailSuppress {
				want = append(want, detail, detail)
			}
			root := strings.Replace(detail, `"parent_tool_use_id":"spawn"`, `"parent_tool_use_id":null`, 1)
			frames = append(frames, root)
			want = append(want, root)
		}
		sendClaudeDetail(s, session, frames...)
		got := drainClaudeDetail(s, session)
		require.Len(t, got, len(want))
		for i, frame := range want {
			require.Equal(t, frame, string(got[i].Raw))
			if got[i].Source.AssociationID != "" {
				require.Equal(t, "task:a", got[i].Source.AssociationID)
				require.Nil(t, got[i].Parent)
			}
		}
	}
}

func checkClaudeMixedDetail(s *claudeChildDetailSuite) {
	t := s.T()
	session, _ := openClaudeDetail(s, claude.ChildDetailSuppress, "")
	early := strings.Replace(claudeDetailText, "child", "early", 1)
	foreign := strings.Replace(claudeDetailText, `"session_id":"s"`, `"session_id":"foreign"`, 1)
	unknown := `{"type":"future","session_id":"s","parent_tool_use_id":"spawn","value":42}`
	sendClaudeDetail(s, session, early, claudeDetailInit, foreign, unknown)
	// Same-session unknown tool/agent identity is insufficient to filter.
	frames := []string{
		strings.Replace(claudeDetailText, "spawn", "missing", 1),
		`{"type":"assistant","session_id":"s","agent_id":"a","message":{"content":[{"type":"text","text":"not proof"}]}}`,
		claudeDetailTask,
	}
	for _, extra := range []string{`"future":null`, `"error":null`, `"tool_use_result":null`, `"agent_id":"unconfirmed"`, `"parent_task_id":"p"`, `"isReplay":true`} {
		frames = append(frames, strings.Replace(claudeDetailText, `"type":"assistant"`, `"type":"assistant",`+extra, 1))
	}
	for _, content := range []string{
		`[{"type":"text","text":"detail"},{"type":"tool_use","id":"nested","name":"Agent","input":{}}]`,
		`[{"type":"text","text":"detail"},{"type":"server_tool_use","id":"server","name":"search","input":{}}]`,
		`[{"type":"text","text":"detail"},{"type":"tool_result","tool_use_id":"nested","content":"result"}]`,
		`[{"type":"future","value":null}]`, `[{"type":"text","text":"x","future":null}]`, `"text shape"`, `[]`,
	} {
		frames = append(frames, `{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn","message":{"content":`+content+`}}`)
	}
	for _, metadata := range []string{`"future":null`, `"usage":{"output_tokens":1,"future":null}`, `"usage":{"output_tokens":null}`, `"role":"user"`} {
		frames = append(frames, `{"type":"assistant","session_id":"s","parent_tool_use_id":"spawn","message":{"content":[{"type":"text","text":"x"}],`+metadata+`}}`)
	}
	for _, event := range []string{
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x","future":null}}`,
		`{"type":"content_block_delta","index":-1,"delta":{"type":"text_delta","text":"x"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"missing index"}}`,
		`{"type":"content_block_delta","delta":{"type":"future","text":"x"}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"},"task_id":"protected"}`,
		`{"type":"message_start","message":{"content":[]}}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_stop"}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_stop","index":0}`, `{"type":"ping"}`, `{"type":"future"}`,
	} {
		frames = append(frames, `{"type":"stream_event","session_id":"s","parent_tool_use_id":"spawn","event":`+event+`}`)
	}
	sendClaudeDetail(s, session, frames...)
	// A reviewed child alias can suppress detail with no known parent. A late
	// root tool owner still creates its protected relationship observation.
	sendClaudeDetail(s, session, claudeDetailText, `{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`)
	got := drainClaudeDetail(s, session)
	require.Len(t, got, len(frames)+2)
	require.Equal(t, claudeDetailInit, string(got[0].Raw)) //nolint:testifylint // preserves exact protocol bytes
	for i, frame := range frames {
		require.Equal(t, frame, string(got[i+1].Raw))
	}
	snap := session.Unattributed()
	require.Len(t, snap.Records, 3)
	for i, want := range []string{early, foreign, unknown} {
		require.Equal(t, want, string(snap.Records[i].Event.Raw))
	}
	// A future version cannot reuse reviewed shapes as a suppression license.
	future, _ := openClaudeDetail(s, claude.ChildDetailSuppress, "")
	futureFrames := []string{strings.Replace(claudeDetailInit, "2.1.293", "future", 1), claudeDetailTask, claudeDetailText}
	sendClaudeDetail(s, future, futureFrames...)
	futureGot := drainClaudeDetail(s, future)
	require.Len(t, futureGot, len(futureFrames))
	require.Empty(t, futureGot[2].Source.AssociationID)
}

func claudeDetailRequest(s *claudeChildDetailSuite, session *claude.Session, id, tool, input string) *claude.IncomingRequest {
	t := s.T()
	t.Helper()
	raw := ` {"type":"control_request","request_id":"` + id + `","request":{"subtype":"can_use_tool","tool_name":"` + tool + `","tool_use_id":"spawn","agent_id":"unverified","input":` + input + `}} `
	sendClaudeDetail(s, session, claudeDetailText, raw)
	for {
		r, err := session.Receive(testsupport.Deadline(t))
		require.NoError(t, err)
		if r.Request != nil {
			require.Equal(t, raw, string(r.Raw))
			require.Equal(t, raw, string(r.Request.Raw()))
			require.Equal(t, r.Params, r.Request.Params())
			require.Equal(t, id, r.Request.ID())
			return r.Request
		}
	}
}

func checkClaudeDetailAuthority(s *claudeChildDetailSuite) {
	t := s.T()
	session, trace := openClaudeDetail(s, claude.ChildDetailSuppress, "")
	sendClaudeDetail(s, session, claudeDetailInit, claudeDetailTask)
	p := claudeDetailRequest(s, session, "original", "Read", `{"file_path":"synthetic","future":null}`)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, p.Approve(canceled, claude.ApprovalOptions{}), context.Canceled)
	require.NoError(t, p.Context().Err())
	outcomes := make(chan error, 12)
	for range 12 {
		go func() { outcomes <- p.Deny(context.Background(), claude.DenialOptions{Message: "declined"}) }()
	}
	successes := 0
	for range 12 {
		err := <-outcomes
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, claude.ErrPermissionUsed)
		}
	}
	require.Equal(t, 1, successes)
	approved := claudeDetailRequest(s, session, "approved", "Read", `{}`)
	require.NoError(t, approved.Approve(testsupport.Deadline(t), claude.ApprovalOptions{}))
	require.ErrorIs(t, approved.Deny(testsupport.Deadline(t), claude.DenialOptions{}), claude.ErrPermissionUsed)
	q := claudeDetailRequest(s, session, "question", "AskUserQuestion", `{"questions":[{"question":"Choose?","header":"Pick","options":[{"label":"A","description":"alpha"}],"multiSelect":false}],"future":null}`)
	require.NoError(t, q.Answer(testsupport.Deadline(t), claude.QuestionAnswers{Answers: map[string]string{"Choose?": "A"}}))
	require.ErrorIs(t, q.Answer(testsupport.Deadline(t), claude.QuestionAnswers{Answers: map[string]string{"Choose?": "A"}}), claude.ErrPermissionUsed)
	revoked := claudeDetailRequest(s, session, "revoked", "Read", `{}`)
	sendClaudeDetail(s, session, `{"type":"control_cancel_request","request_id":"revoked"}`)
	require.ErrorIs(t, revoked.Context().Err(), context.Canceled)
	require.ErrorIs(t, revoked.Approve(testsupport.Deadline(t), claude.ApprovalOptions{}), claude.ErrPermissionCancelled)
	pending := claudeDetailRequest(s, session, "pending", "Read", `{}`)
	drainClaudeDetail(s, session)
	require.ErrorIs(t, pending.Deny(testsupport.Deadline(t), claude.DenialOptions{}), claude.ErrClosed)
	raw, err := os.ReadFile(trace)
	require.NoError(t, err)
	var replies []clt.ControlResponse
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n")[1:] {
		var f struct{ Type string }
		require.NoError(t, json.Unmarshal([]byte(line), &f))
		if f.Type == "control_response" {
			var r clt.ControlResponse
			require.NoError(t, json.Unmarshal([]byte(line), &r))
			replies = append(replies, r)
		}
	}
	require.Len(t, replies, 3)
	require.Equal(t, "original", replies[0].Response.RequestID)
	require.Equal(t, "approved", replies[1].Response.RequestID)
	require.Equal(t, "question", replies[2].Response.RequestID)
	require.Contains(t, string(replies[2].Response.Response), `"answers":{"Choose?":"A"}`)
}

func checkClaudeDetailLifecycle(s *claudeChildDetailSuite) {
	t := s.T()
	for _, abort := range []bool{false, true} {
		session, _ := openClaudeDetail(s, claude.ChildDetailSuppress, "")
		frames := []string{
			claudeDetailInit,
			`{"type":"assistant","session_id":"s","message":{"content":[{"type":"tool_use","id":"spawn","name":"Agent","input":{}}]}}`,
			claudeDetailTask,
			`{"type":"system","subtype":"task_started","session_id":"s","task_id":"b","task_type":"local_agent","tool_use_id":"sibling","is_backgrounded":true}`,
			`{"type":"user","session_id":"s","message":{"content":[{"type":"tool_result","tool_use_id":"spawn","content":"child result","is_error":true}]},"tool_use_result":{"status":"failed"}}`,
			`{"type":"result","session_id":"s","result":"root done"}`,
			`{"type":"system","subtype":"task_progress","session_id":"s","task_id":"b","summary":"background"}`,
			`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"a","status":"completed"}`,
			`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"b","status":"failed"}`,
			`{"type":"system","subtype":"task_notification","session_id":"s","task_id":"b","status":"killed"}`,
			`{"type":"system","subtype":"background_tasks_changed","session_id":"s","tasks":[]}`,
		}
		var sent []string
		for i, frame := range frames {
			sent = append(sent, frame)
			if i < 3 {
				continue
			}
			for range 40 {
				sent = append(sent, claudeDetailText, strings.Replace(claudeDetailText, "spawn", "sibling", 1))
			}
		}
		// A control response after a burst is a reader barrier with Receive unread.
		sendClaudeDetail(s, session, sent...)
		if abort {
			require.NoError(t, session.Abort())
		}
		got := drainClaudeDetail(s, session)
		if abort {
			require.Empty(t, got)
			continue
		}
		require.Len(t, got, len(frames))
		for i, frame := range frames {
			require.Equal(t, frame, string(got[i].Raw))
		}
		require.Equal(t, "task:a", got[7].Source.AssociationID)
		require.NotNil(t, got[7].Parent)
		require.Equal(t, "task:b", got[8].Source.AssociationID)
		require.Nil(t, got[8].Parent)
	}
}
