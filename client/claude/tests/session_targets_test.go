package claude_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	clt "github.com/zoumo/agent-sdk-go/client/claude/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/nativepeer"
)

func TestSessionTargets(t *testing.T) {
	for _, scenario := range []string{"session-persistent-inputs", "session-native-input-fields", "session-submit-rejection", "session-tool-completion", "session-unknown-events", "session-child-association", "session-child-depth", "session-subagent-query", "session-approval", "session-canceled-approval", "session-question", "session-model-change", "session-thinking-change", "session-interrupt", "session-slow-consumer", "session-resume", "session-error-result", "session-close"} {
		t.Run(scenario, func(t *testing.T) { claudeSessionTarget(t, scenario) })
	}
}

func claudeTargetOptions(t *testing.T, scenario string) (claude.SessionOptions, string) {
	t.Helper()
	exe, e := os.Executable()
	require.NoError(t, e)
	dir := t.TempDir()
	dir, e = filepath.EvalSymlinks(dir)
	require.NoError(t, e)
	path := filepath.Join(dir, "wire.jsonl")
	return claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, "config"), "SDK_INTEGRATION_PEER=claude", "SDK_INTEGRATION=" + scenario, "SDK_INTEGRATION_TRACE=" + path, "GORACE=atexit_sleep_ms=0"}}, path
}

func claudeUntil(t *testing.T, s *claude.Session, subtype string) []claude.Received {
	t.Helper()
	ctx := testsupport.Deadline(t)
	var all []claude.Received
	for {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		all = append(all, r)
		if r.Event != nil && (r.Event.Subtype == subtype || claudetest.FixtureKind(r.Raw) == subtype || subtype == "fixture_tool" && r.Event.Type == "user") {
			return all
		}
	}
}

func claudeSessionTarget(t *testing.T, scenario string) {
	o, path := claudeTargetOptions(t, scenario)
	var barrier *net.TCPListener
	if scenario == "session-slow-consumer" {
		barrier = faultBarrier(t)
		o.Env = append(o.Env, "SDK_INTEGRATION_BARRIER="+barrier.Addr().String())
	}
	if scenario == "session-model-change" || scenario == "session-thinking-change" {
		budget := 1024
		display := "summarized"
		o.Model = "creation-model"
		o.Thinking = &clt.ThinkingConfig{Type: "enabled", BudgetTokens: &budget, Display: &display}
	}
	s, e := claude.Open(testsupport.Deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = s.Close() })
	ctx := testsupport.Deadline(t)
	claudeUntil(t, s, "init")
	switch scenario {
	case "session-persistent-inputs":
		for range 2 {
			v, err := s.Submit(ctx, claude.Input{Text: "next"})
			require.NoError(t, err)
			require.True(t, v.Written)
			got := claudeUntil(t, s, "success")
			require.Equal(t, "result", got[len(got)-1].Event.Type)
			select {
			case <-s.Done():
				t.Fatal("result ended Session")
			default:
			}
		}
	case "session-native-input-fields":
		var u clt.UserInput
		require.NoError(t, json.Unmarshal([]byte(`{"message":{"role":"user","content":[{"type":"text","text":"exact"},{"type":"image","source":{"type":"url","url":"https://fixture.invalid/i"},"future":null},{"type":"document","source":{"type":"text","data":"doc","media_type":"text/plain"}},{"type":"tool_result","tool_use_id":"original","content":"native"}]},"priority":"later","future":{"n":9007199254740993}}`), &u))
		_, e = s.Submit(ctx, claude.Input{User: &u})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_submitted")
		wire := testsupport.ObservedWire(t, all[len(all)-1].Raw)
		assert.Empty(t, wire["session_id"])
		assert.Nil(t, wire["parent_tool_use_id"])
		assert.Equal(t, "later", wire["priority"])
		assert.JSONEq(t, `{"role":"user","content":[{"type":"text","text":"exact"},{"type":"image","source":{"type":"url","url":"https://fixture.invalid/i"},"future":null},{"type":"document","source":{"type":"text","data":"doc","media_type":"text/plain"}},{"type":"tool_result","tool_use_id":"original","content":"native"}]}`, string(testsupport.Marshal(t, wire["message"])))
		assert.JSONEq(t, `{"n":9007199254740993}`, string(testsupport.Marshal(t, wire["future"])))
		assert.Contains(t, string(all[len(all)-1].Raw), `9007199254740993`)
		require.NoError(t, json.Unmarshal([]byte(`{"session_id":"child","message":{"role":"user","content":"forbidden"}}`), &u))
		_, e = s.Submit(ctx, claude.Input{User: &u})
		require.ErrorIs(t, e, claude.ErrInvalidInput)
		require.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
	case "session-submit-rejection":
		// Native stream-json gives no submit acceptance RPC. Continue writing while
		// the first execution has no result; a native failure remains a received fact.
		for range 2 {
			v, err := s.Submit(ctx, claude.Input{Text: "supplement"})
			require.NoError(t, err)
			require.True(t, v.Written)
			claudeUntil(t, s, "fixture_submitted")
		}
		all := claudeUntil(t, s, "success")
		require.True(t, all[len(all)-1].Event.Result.IsError)
		assert.NoError(t, s.Err())
		require.Equal(t, 2, testsupport.CountFrames(t, path, "user"))
	case "session-tool-completion":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_tool")
		tool := all[len(all)-1].Event
		require.NotNil(t, tool.User)
		require.Len(t, tool.User.Content.Blocks, 1)
		require.NotNil(t, tool.User.Content.Blocks[0].ToolResult)
		assert.Equal(t, "tool", tool.User.Content.Blocks[0].ToolResult.ToolUseID)
		require.Contains(t, string(tool.Raw), `"content":"ok"`)
		select {
		case <-s.Done():
			t.Fatal("tool ended Session")
		default:
		}
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		all = append(all, claudeUntil(t, s, "success")...)
		require.False(t, all[len(all)-1].Event.Result.IsError)
		var kinds []string
		for _, r := range all {
			if r.Event != nil {
				kinds = append(kinds, r.Event.Type)
			}
		}
		assert.Equal(t, []string{"system", "user", "result"}, kinds)
		select {
		case <-s.Done():
			t.Fatal("result ended Session")
		default:
		}
	case "session-unknown-events":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_barrier")
		var raw []string
		for _, r := range all {
			if r.Event != nil && claudetest.FixtureKind(r.Raw) == "future_repeated" {
				require.Equal(t, "live", r.Origin)
				require.Contains(t, string(r.Raw), `"n":9007199254740993`)
				raw = append(raw, string(r.Raw))
			}
		}
		require.Len(t, raw, 2)
		assert.Equal(t, raw[0], raw[1])
		d := s.Unattributed()
		require.True(t, d.CoverageGap)
		require.Len(t, d.Records, 1)
		assert.Equal(t, "future_envelope", d.Records[0].Event.Type)

	case "session-child-association", "session-child-depth":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_barrier")
		var sources []string
		var parentUnknown, nested, rootResult, childAfter, gap bool
		for _, r := range all {
			require.Nil(t, r.Request, "relationship minted answer authority")
			if r.Event != nil {
				if r.Event.Type == "result" {
					rootResult = true
				}
				if r.Event.Type == "assistant" && r.Source.TaskID != "" {
					sources = append(sources, r.Source.TaskID)
					if r.Source.TaskID == "child" && r.Parent == nil {
						parentUnknown = true
					}
					if r.Source.TaskID == "grand" && r.Parent != nil && r.Parent.TaskID == "child" {
						nested = true
					}
					if rootResult && r.Source.TaskID == "grand" {
						childAfter = true
					}
				}
			}
			if r.Observation != nil && (r.Observation.Reason == "forwarding-not-confirmed" || r.Observation.Reason == "identity-member-unconfirmed") {
				gap = true
			}
		}
		// Open already delivered its explicit forwarding gap before init.
		assert.True(t, parentUnknown)
		assert.True(t, nested)
		assert.True(t, childAfter)
		assert.Contains(t, sources, "sibling")
		assert.True(t, gap)
		d := s.Unattributed()
		assert.True(t, d.CoverageGap)
		require.Len(t, d.Records, 1)
		assert.Equal(t, "foreign", d.Records[0].Event.SessionID)
		require.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
	case "session-subagent-query":
		dir := prepareClaudeRecords(t, o)
		page, err := s.ListSubagents(ctx, claude.SubagentQuery{MaxRecords: 1})
		require.ErrorIs(t, err, claude.ErrDiscoveryCoverage)
		require.Len(t, page.Records, 1)
		assert.Equal(t, "partial", page.Coverage.Status)
		full, err := s.ListSubagents(ctx, claude.SubagentQuery{})
		require.NoError(t, err)
		require.Len(t, full.Records, 2)
		assert.Equal(t, "complete", full.Coverage.Status)
		assert.False(t, full.Coverage.Atomic)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-new.jsonl"), []byte(`{"type":"assistant","message":{"content":"stored"}}`+"\n"), 0o600))
		fresh, err := s.ListSubagents(ctx, claude.SubagentQuery{})
		require.NoError(t, err)
		require.Len(t, fresh.Records, 3)
		for _, r := range fresh.Records {
			assert.Equal(t, "unknown", r.Running)
		}
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		all := claudeUntil(t, s, "fixture_interrupt")
		require.Len(t, all, 1)
		require.Zero(t, testsupport.CountFrames(t, path, "user"))
		assert.Zero(t, testsupport.CountAnswers(t, path, "claude"))
	case "session-approval", "session-canceled-approval", "session-question":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		requests := claudetest.Requests(t, s, 2)
		require.Contains(t, string(requests[0].Raw()), "future_envelope")
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		assert.Zero(t, testsupport.CountAnswers(t, path, "claude"))
		switch scenario {
		case "session-canceled-approval":
			testsupport.Wait(t, ctx, requests[0].Context().Done())
			require.Error(t, requests[0].Deny(ctx, claude.DenialOptions{}))
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, requests[1].Approve(canceled, claude.ApprovalOptions{}), context.Canceled)
			require.NoError(t, requests[1].Approve(ctx, claude.ApprovalOptions{}))
			require.Error(t, requests[1].Approve(ctx, claude.ApprovalOptions{}))
		case "session-question":
			for i, r := range requests {
				answer := "A"
				if i == 1 {
					answer = "free text / 自由文本"
				}
				require.NoError(t, r.Answer(ctx, claude.QuestionAnswers{Answers: map[string]string{"Choose?": answer}}))
				require.Error(t, r.Answer(ctx, claude.QuestionAnswers{Answers: map[string]string{"Choose?": answer}}))
			}
		default:
			stop := true
			require.NoError(t, requests[0].Deny(ctx, claude.DenialOptions{Message: "user refused", Interrupt: &stop}))
			require.NoError(t, requests[1].Approve(ctx, claude.ApprovalOptions{UpdatedInput: json.RawMessage(`{"command":"echo modified","future":42}`), UpdatedPermissions: []clt.PermissionUpdate{{Type: "setMode", Mode: "acceptEdits", Destination: "session"}}}))
			require.Error(t, requests[1].Deny(ctx, claude.DenialOptions{}))
		}
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		n := 2
		if scenario == "session-canceled-approval" {
			n = 1
		}
		assert.Equal(t, n, testsupport.CountAnswers(t, path, "claude"))
		require.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
		testsupport.CheckAnswerWires(t, path, scenario, "claude")
		want := []string{"initialize", "user", "interrupt"}
		if scenario != "session-canceled-approval" {
			want = append(want, "answer:first")
		}
		want = append(want, "answer:second", "interrupt")
		testsupport.CheckFaultControlOrder(t, path, "claude", want)

	case "session-model-change", "session-thinking-change":
		_, e = s.Submit(ctx, claude.Input{Text: "executing"})
		require.NoError(t, e)
		claudeUntil(t, s, "fixture_submitted")
		model := "requested-model"
		receipt, err := s.SetModel(ctx, claude.ModelChange{Model: &model})
		require.NoError(t, err)
		require.True(t, receipt.Accepted)
		assert.Nil(t, receipt.Observed)
		assert.JSONEq(t, `{"subtype":"set_model","model":"requested-model"}`, string(receipt.Requested))
		assert.Contains(t, string(receipt.Response), `"success"`)
		zero := 0
		display := "omitted"
		thinking, err := s.SetThinking(ctx, claude.ThinkingChange{MaxThinkingTokens: &zero, ThinkingDisplay: &display})
		require.NoError(t, err)
		assert.True(t, thinking.Accepted)
		assert.Nil(t, thinking.Observed)
		assert.JSONEq(t, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":0,"thinking_display":"omitted"}`, string(thinking.Requested))
		cleared, err := s.SetThinking(ctx, claude.ThinkingChange{ClearDisplay: true})
		require.NoError(t, err)
		assert.JSONEq(t, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null,"thinking_display":null}`, string(cleared.Requested))
		def, err := s.SetModel(ctx, claude.ModelChange{})
		require.NoError(t, err)
		assert.JSONEq(t, `{"subtype":"set_model"}`, string(def.Requested))
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		all := claudeUntil(t, s, "init")
		require.Equal(t, "observed-model", all[len(all)-1].Event.System.Model)
		model = "native-reject"
		_, e = s.SetModel(ctx, claude.ModelChange{Model: &model})
		var native *claude.ControlError
		require.ErrorAs(t, e, &native)
		assert.Contains(t, native.Error(), "native restriction")
		bad := "adaptive"
		_, e = s.SetThinking(ctx, claude.ThinkingChange{ThinkingDisplay: &bad})
		require.ErrorIs(t, e, claude.ErrUnsupported)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "launch"))
		frames := testsupport.PeerFrames(t, path)
		var launch struct{ Args []string }
		require.NoError(t, json.Unmarshal(frames[0], &launch))
		assert.Contains(t, launch.Args, "--model=creation-model")
		assert.Contains(t, launch.Args, "--max-thinking-tokens=1024")
		assert.Contains(t, launch.Args, "--thinking-display=summarized")
		var controls []json.RawMessage
		for _, b := range frames {
			var f struct {
				Type    string
				Request struct{ Subtype string }
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Type == "control_request" && (f.Request.Subtype == "set_model" || f.Request.Subtype == "set_max_thinking_tokens") {
				var w struct{ Request json.RawMessage }
				require.NoError(t, json.Unmarshal(b, &w))
				controls = append(controls, w.Request)
			}
		}
		require.Len(t, controls, 5)
		for i, want := range []string{`{"subtype":"set_model","model":"requested-model"}`, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":0,"thinking_display":"omitted"}`, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null,"thinking_display":null}`, `{"subtype":"set_model"}`, `{"subtype":"set_model","model":"native-reject"}`} {
			assert.JSONEq(t, want, string(controls[i]))
		}
		assert.NoError(t, s.Err())
	case "session-interrupt":
		_, e = s.Submit(ctx, claude.Input{Text: "burst"})
		require.NoError(t, e)
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		require.NoError(t, s.Close())
		testsupport.Wait(t, ctx, s.Done())
		var sequence []int
		for {
			r, err := s.Receive(ctx)
			if err != nil {
				require.ErrorIs(t, err, io.EOF)
				break
			}
			if r.Event != nil && claudetest.FixtureKind(r.Raw) == "fixture_burst" {
				var p struct{ Sequence int }
				require.NoError(t, json.Unmarshal(r.Raw, &p))
				sequence = append(sequence, p.Sequence)
			}
		}
		require.Len(t, sequence, 256)
		for i, v := range sequence {
			assert.Equal(t, i, v)
		}
		assert.NoError(t, s.Err())
	case "session-slow-consumer":
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e = s.Submit(canceled, claude.Input{Text: "not sent"})
		var notSent *claude.DispatchError
		require.ErrorAs(t, e, &notSent)
		assert.Equal(t, claude.NotSent, notSent.State)
		_, e = s.Receive(canceled)
		require.ErrorIs(t, e, context.Canceled)
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		_, e = s.Submit(ctx, claude.Input{Text: "prepare blocked input"})
		require.NoError(t, e)
		claudeUntil(t, s, "fixture_submitted")
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		go func() { _, err := s.Submit(waiting, claude.Input{Text: strings.Repeat("x", 2<<20)}); result <- err }()
		blocked := acceptFaultBarrier(t, barrier, '{')
		stop()
		e = <-result
		var unknown *claude.DispatchError
		require.ErrorAs(t, e, &unknown)
		assert.Equal(t, claude.OutcomeUnknown, unknown.State)
		require.ErrorIs(t, e, claude.ErrUnknownOutcome)
		_, e = blocked.Write([]byte("r"))
		require.NoError(t, e)
		require.ErrorIs(t, s.Close(), claude.ErrUnknownOutcome)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
		// Local Receive cancellation was checked before dispatch, while Session is live.
	case "session-resume":
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		select {
		case <-s.Done():
			t.Fatal("Interrupt closed Session")
		default:
		}
		_, e = s.Submit(ctx, claude.Input{Text: "after interrupt"})
		require.NoError(t, e)
		claudeUntil(t, s, "fixture_submitted")
		require.NoError(t, s.Close())
		testsupport.Wait(t, ctx, s.Done())
		require.NoError(t, s.Close())
		require.Error(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "launch"))
	case "session-error-result":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		requests := claudetest.Requests(t, s, 1)
		all := claudeUntil(t, s, "success")
		assert.True(t, all[len(all)-1].Event.Result.IsError)
		assert.NoError(t, s.Err())
		_ = s.Interrupt(ctx, claude.InterruptOptions{})
		testsupport.Wait(t, ctx, s.Done())
		cause := s.Err()
		require.ErrorIs(t, cause, claude.ErrUnexpectedEOF)
		require.Error(t, requests[0].Approve(ctx, claude.ApprovalOptions{}))
		drained := 0
		for {
			r, err := s.Receive(ctx)
			if err != nil {
				require.ErrorIs(t, err, claude.ErrUnexpectedEOF)
				break
			}
			if r.Event != nil && claudetest.FixtureKind(r.Raw) == "fixture_burst" {
				var payload struct {
					Sequence int `json:"sequence"`
				}
				require.NoError(t, json.Unmarshal(r.Raw, &payload))
				assert.Equal(t, drained, payload.Sequence, "accepted native facts require.NoError drain in order")
				drained++
			}
		}
		assert.Equal(t, 32, drained)
		_ = s.Close()
		// Process cleanup may wrap the original EOF after Done closes.
		assert.ErrorIs(t, s.Err(), claude.ErrUnexpectedEOF)
	case "session-close":
		_, e = s.Submit(ctx, claude.Input{Text: "disconnect"})
		require.NoError(t, e)
		testsupport.Wait(t, ctx, s.Done())
		cause := s.Err()
		require.ErrorIs(t, cause, claude.ErrUnexpectedEOF)
		drained := 0
		for {
			r, err := s.Receive(ctx)
			if err != nil {
				require.ErrorIs(t, err, claude.ErrUnexpectedEOF)
				break
			}
			if r.Event != nil {
				drained++
				assert.Equal(t, "fixture_submitted", claudetest.FixtureKind(r.Raw))
			}
		}
		assert.Equal(t, 1, drained)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "launch"))
		_, e = s.Submit(ctx, claude.Input{Text: "no retry"})
		require.Error(t, e)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "user"))
	default:
		t.Fatalf("missing native acceptance for %s", scenario)
	}
}

func prepareClaudeRecords(t *testing.T, o claude.SessionOptions) string {
	t.Helper()
	config := ""
	for _, v := range o.Env {
		if after, ok := strings.CutPrefix(v, "CLAUDE_CONFIG_DIR="); ok {
			config = after
		}
	}
	key := regexp.MustCompile(`[^a-zA-Z0-9-]`).ReplaceAllString(o.WorkDir, "-")
	base := filepath.Join(config, "projects", key)
	sub := filepath.Join(base, nativepeer.ClaudeSessionID, "subagents")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, "nested"), 0o700))
	main := fmt.Sprintf(`{"type":"user","uuid":"u","parentUuid":null,"sessionId":%q,"cwd":%q,"message":{"role":"user","content":"stored question is not live authority"}}`, nativepeer.ClaudeSessionID, o.WorkDir)
	require.NoError(t, os.WriteFile(filepath.Join(base, nativepeer.ClaudeSessionID+".jsonl"), []byte(main+"\n"), 0o600))
	for _, name := range []string{"agent-old.jsonl", "nested/agent-nested.jsonl"} {
		require.NoError(t, os.WriteFile(filepath.Join(sub, name), []byte(`{"type":"assistant","message":{"content":"retained"}}`+"\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested", "agent-nested.meta.json"), []byte(`{"parentAgentId":"old","toolUseId":"spawn-nested","future":null}`), 0o600))
	return sub
}

// faultBarrier is a bounded application/peer handshake, separate from native
// traffic. It proves a write prefix was read before cancellation, without polls.
func faultBarrier(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	d, ok := testsupport.Deadline(t).Deadline()
	require.True(t, ok)
	require.NoError(t, l.SetDeadline(d))
	return l
}

func acceptFaultBarrier(t *testing.T, l *net.TCPListener, want byte) net.Conn {
	t.Helper()
	c, err := l.Accept()
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	d, ok := testsupport.Deadline(t).Deadline()
	require.True(t, ok)
	require.NoError(t, c.SetDeadline(d))
	var got [1]byte
	_, err = io.ReadFull(c, got[:])
	require.NoError(t, err)
	require.Equal(t, want, got[0])
	return c
}
