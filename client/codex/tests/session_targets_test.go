package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	codextest "github.com/zoumo/agent-sdk-go/internal/testsupport/codex"
)

func TestSessionTargets(t *testing.T) {
	for _, scenario := range []string{"session-persistent-inputs", "session-native-input-fields", "session-submit-rejection", "session-tool-completion", "session-unknown-events", "session-child-association", "session-child-depth", "session-subagent-query", "session-approval", "session-canceled-approval", "session-question", "session-model-change", "session-thinking-change", "session-interrupt", "session-slow-consumer", "session-resume", "session-error-result", "session-close"} {
		t.Run(scenario, func(t *testing.T) { codexSessionTarget(t, scenario) })
	}
}

func codexTargetOptions(t *testing.T, scenario string) (codex.StdioOptions, string) {
	t.Helper()
	exe, e := os.Executable()
	require.NoError(t, e)
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	return codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_INTEGRATION_PEER=codex", "SDK_INTEGRATION=" + scenario, "SDK_INTEGRATION_TRACE=" + path, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "session-integration", Version: "synthetic"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}}, path
}

func codexUntil(t *testing.T, s *codex.Session, method string) []codex.Received {
	t.Helper()
	ctx := testsupport.Deadline(t)
	var all []codex.Received
	for {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		all = append(all, r)
		if r.Event != nil {
			require.NoError(t, r.Event.DecodeError)
		}
		if r.Event != nil && r.Event.Method == method {
			return all
		}
	}
}

func codexSessionTarget(t *testing.T, scenario string) {
	o, path := codexTargetOptions(t, scenario)
	c, e := codex.NewStdio(testsupport.Deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = c.Close() })
	creation := ct.ThreadStartParams{}
	if scenario == "session-model-change" || scenario == "session-thinking-change" {
		creation.Model = ct.Some("creation-model")
		creation.Config = ct.Some(map[string]json.RawMessage{"model_reasoning_effort": json.RawMessage(`"medium"`)})
	}
	s, e := codex.Open(testsupport.Deadline(t), codex.SessionOptions{Client: c, MaxParentDepth: 1, Thread: creation})
	require.NoError(t, e)
	t.Cleanup(func() { _ = s.Close() })
	ctx := testsupport.Deadline(t)
	switch scenario {
	case "session-persistent-inputs":
		_, e = codex.Open(ctx, codex.SessionOptions{Client: c})
		require.ErrorIs(t, e, codex.ErrOwnershipConflict)
		for range 2 {
			v, err := s.Submit(ctx, codex.Input{})
			require.NoError(t, err)
			require.Equal(t, "turn", v.Response.Turn.ID)
			got := codexUntil(t, s, "turn/completed")
			require.Equal(t, "live", got[len(got)-1].Origin)
			select {
			case <-s.Done():
				t.Fatal("result ended Session")
			default:
			}
		}
	case "session-native-input-fields":
		var input ct.UserInput
		require.NoError(t, json.Unmarshal([]byte(`{"type":"image","url":"https://fixture.invalid/image.png"}`), &input))
		_, e = s.Submit(ctx, codex.Input{Input: []ct.UserInput{input}, Model: ct.Some("native-model"), OutputSchema: ct.Some(json.RawMessage(`{"type":"object","x":null}`))})
		require.NoError(t, e)
		r := codexUntil(t, s, "fixture/submitted")
		wire := testsupport.ObservedWire(t, r[len(r)-1].Params)
		assert.Equal(t, "root", wire["threadId"])
		assert.Equal(t, "native-model", wire["model"])
		assert.JSONEq(t, `[{"type":"image","url":"https://fixture.invalid/image.png"}]`, string(testsupport.Marshal(t, wire["input"])))
		assert.JSONEq(t, `{"type":"object","x":null}`, string(testsupport.Marshal(t, wire["outputSchema"])))
		_, e = s.Submit(ctx, codex.Input{Input: []ct.UserInput{{Unknown: json.RawMessage(`{"type":"future"}`)}}})
		require.ErrorIs(t, e, codex.ErrInvalidInput)
		require.Equal(t, 1, testsupport.CountFrames(t, path, "turn/start"))
	case "session-submit-rejection":
		for range 2 {
			_, e = s.Submit(ctx, codex.Input{})
			require.NoError(t, e)
			codexUntil(t, s, "fixture/submitted")
		}
		_, e = s.Submit(ctx, codex.Input{})
		var rejected *codex.RPCError
		require.ErrorAs(t, e, &rejected)
		assert.EqualValues(t, -32001, rejected.Code)
		assert.NoError(t, s.Err())
		require.Equal(t, 3, testsupport.CountFrames(t, path, "turn/start"))
	case "session-tool-completion":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		all := codexUntil(t, s, "item/completed")
		tool := all[len(all)-1].Event.Data.(*ct.ItemCompletedNotification)
		require.NotNil(t, tool.Item.CommandExecution)
		assert.Equal(t, "tool", tool.Item.CommandExecution.ID)
		assert.Equal(t, "ok", tool.Item.CommandExecution.AggregatedOutput.ValueOr(""))
		assert.Equal(t, ct.CommandExecutionStatusCompleted, tool.Item.CommandExecution.Status)
		select {
		case <-s.Done():
			t.Fatal("tool ended Session")
		default:
		}
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		all = append(all, codexUntil(t, s, "turn/completed")...)
		result := all[len(all)-1].Event.Data.(*ct.TurnCompletedNotification)
		assert.Equal(t, ct.TurnStatusCompleted, result.Turn.Status)
		var methods []string
		for _, r := range all {
			if r.Event != nil {
				methods = append(methods, r.Event.Method)
			}
		}
		assert.Equal(t, []string{"fixture/submitted", "item/completed", "turn/completed"}, methods)
		select {
		case <-s.Done():
			t.Fatal("native result ended interaction")
		default:
		}
	case "session-unknown-events":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		all := codexUntil(t, s, "fixture/barrier")
		var raw []string
		for _, r := range all {
			if r.Event != nil && r.Event.Method == "future/repeated" {
				require.Equal(t, "live", r.Origin)
				require.Contains(t, string(r.Raw), `"n":9007199254740993`)
				raw = append(raw, string(r.Raw))
			}
		}
		require.Len(t, raw, 2)
		assert.Equal(t, raw[0], raw[1])

	case "session-child-association", "session-child-depth":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		all := codexUntil(t, s, "fixture/barrier")
		var sources []string
		var rootResult, childAfter, gap bool
		for _, r := range all {
			require.Nil(t, r.Request, "relationship minted answer authority")
			if r.Event != nil {
				if r.Event.Method == "turn/completed" {
					rootResult = true
				}
				if r.Event.Method == "item/agentMessage/delta" {
					sources = append(sources, r.Source.ThreadID)
					if rootResult && r.Source.ThreadID == "grand" {
						childAfter = true
					}
				}
			}
			if r.Observation != nil {
				if strings.Contains(r.Observation.Reason, "depth limit") || r.Observation.Reason == "communication-target-outside-confirmed-scope" {
					gap = true
				}
				assert.False(t, r.Observation.ThreadID == "external" && r.Observation.ParentThreadID != "", "communication invented parent")
			}
		}
		assert.Equal(t, []string{"grand", "child", "sibling", "grand"}, sources)
		assert.True(t, gap)
		assert.True(t, childAfter)
		d := s.Unattributed()
		require.True(t, d.CoverageGap)
		require.Len(t, d.Records, 2)
		assert.NoError(t, s.Err())
		require.Zero(t, testsupport.CountFrames(t, path, "thread/resume"))
		require.Zero(t, testsupport.CountFrames(t, path, "thread/read"))
	case "session-subagent-query":
		page, err := s.ListSubagents(ctx, codex.SubagentQuery{MaxPages: 1, Limit: ct.Some(uint32(1))})
		require.ErrorIs(t, err, codex.ErrLimit)
		require.Len(t, page.Pages, 1)
		assert.False(t, page.Coverage.EnumerationComplete)
		assert.Equal(t, "p2", page.NextCursor.ValueOr(""))
		full, err := s.ListSubagents(ctx, codex.SubagentQuery{Limit: ct.Some(uint32(1))})
		require.NoError(t, err)
		require.Len(t, full.Pages, 2)
		assert.True(t, full.Coverage.EnumerationComplete)
		assert.Equal(t, "stored-a", full.Pages[0].Data[0].ID)
		assert.False(t, full.Coverage.Archived)
		archived, err := s.ListSubagents(ctx, codex.SubagentQuery{Archived: ct.Some(true)})
		require.NoError(t, err)
		assert.True(t, archived.Coverage.Archived)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		all := codexUntil(t, s, "fixture/interrupt")
		require.Len(t, all, 1)
		for _, b := range testsupport.PeerFrames(t, path) {
			var f struct {
				Method string
				Params map[string]json.RawMessage
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Method == "thread/list" {
				assert.JSONEq(t, `"root"`, string(f.Params["ancestorThreadId"]))
				assert.NotContains(t, f.Params, "parentThreadId")
			} else {
				assert.NotContains(t, []string{"thread/resume", "turn/start", "thread/read"}, f.Method)
			}
		}
	case "session-approval", "session-canceled-approval", "session-question":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		requests := codextest.Requests(t, s, 2)
		require.Contains(t, string(requests[0].Raw()), "future_envelope")
		require.Contains(t, string(requests[0].Params()), `"future":42`)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		assert.Zero(t, testsupport.CountAnswers(t, path, "codex"), "unanswered request was automatically decided")
		switch scenario {
		case "session-canceled-approval":
			testsupport.Wait(t, ctx, requests[0].Context().Done())
			require.Error(t, requests[0].Deny(ctx, codex.ApprovalOptions{}))
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, requests[1].Approve(canceled, codex.ApprovalOptions{}), context.Canceled)
			require.NoError(t, requests[1].Approve(ctx, codex.ApprovalOptions{}))
			require.Error(t, requests[1].Approve(ctx, codex.ApprovalOptions{}))
		case "session-question":
			for i, r := range requests {
				answer := []string{"A"}
				if i == 1 {
					answer = []string{"free text / 自由文本"}
				}
				require.ErrorIs(t, r.Approve(ctx, codex.ApprovalOptions{}), codex.ErrInvalidInput)
				require.NoError(t, r.Answer(ctx, ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{"q": {Answers: answer}}}))
				require.Error(t, r.Answer(ctx, ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{"q": {Answers: answer}}}))
			}
		default:
			require.NoError(t, requests[0].Deny(ctx, codex.ApprovalOptions{}))
			var ext ct.CommandExecutionRequestApprovalResponse
			require.NoError(t, json.Unmarshal([]byte(`{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["echo","fixture"]}}}`), &ext))
			require.NoError(t, requests[1].Approve(ctx, codex.ApprovalOptions{CommandExecution: &ext}))
			require.Error(t, requests[1].Deny(ctx, codex.ApprovalOptions{}))
		}
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		n := 2
		if scenario == "session-canceled-approval" {
			n = 1
		}
		assert.Equal(t, n, testsupport.CountAnswers(t, path, "codex"))
		require.Equal(t, 1, testsupport.CountFrames(t, path, "turn/start"))
		testsupport.CheckAnswerWires(t, path, scenario, "codex")
		want := []string{"initialize", "initialized", "thread/start", "turn/start", "turn/interrupt"}
		if scenario != "session-canceled-approval" {
			want = append(want, "answer:first")
		}
		want = append(want, "answer:second", "turn/interrupt")
		testsupport.CheckFaultControlOrder(t, path, "codex", want)

	case "session-model-change", "session-thinking-change":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		codexUntil(t, s, "fixture/submitted")
		receipt, err := s.SetModel(ctx, "requested-model")
		require.NoError(t, err)
		require.True(t, receipt.NativeAccepted)
		assert.Equal(t, "requested-model", receipt.Requested.Model.ValueOr(""))
		assert.Equal(t, "root", receipt.Requested.ThreadID)
		effort := ct.ReasoningEffort("high")
		thinking, err := s.SetThinking(ctx, codex.ThinkingChange{Effort: ct.Some(effort)})
		require.NoError(t, err)
		assert.True(t, thinking.NativeAccepted)
		assert.Equal(t, effort, thinking.Requested.Effort.ValueOr(""))
		// Ack contains no observed settings; a later native fact intentionally differs.
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		all := codexUntil(t, s, "thread/settings/updated")
		r := all[len(all)-1]
		assert.Contains(t, string(r.Params), `"model":"observed-model"`)
		assert.NotContains(t, string(r.Params), `requested-model`)
		_, e = s.SetModel(ctx, "native-reject")
		var native *codex.RPCError
		require.ErrorAs(t, e, &native)
		assert.EqualValues(t, -32602, native.Code)
		_, e = s.SetModel(ctx, "")
		require.ErrorIs(t, e, codex.ErrInvalidInput)
		for _, b := range testsupport.PeerFrames(t, path) {
			var f struct {
				Method string
				Params map[string]json.RawMessage
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Method == "thread/settings/update" {
				assert.JSONEq(t, `"root"`, string(f.Params["threadId"]))
			}
		}
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "thread/start"))
		assert.Equal(t, 3, testsupport.CountFrames(t, path, "thread/settings/update"))
		var starts, settings []json.RawMessage
		for _, b := range testsupport.PeerFrames(t, path) {
			var f struct {
				Method string
				Params json.RawMessage
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Method == "thread/start" {
				starts = append(starts, f.Params)
			}
			if f.Method == "thread/settings/update" {
				settings = append(settings, f.Params)
			}
		}
		require.Len(t, starts, 1)
		assert.JSONEq(t, `{"model":"creation-model","config":{"model_reasoning_effort":"medium"}}`, string(starts[0]))
		assert.JSONEq(t, `{"threadId":"root","model":"requested-model"}`, string(settings[0]))
		assert.JSONEq(t, `{"threadId":"root","effort":"high"}`, string(settings[1]))
	case "session-interrupt":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"})) // all 256 frames are unread
		require.NoError(t, s.Close())
		testsupport.Wait(t, ctx, s.Done())
		var sequence []int
		for {
			r, err := s.Receive(ctx)
			if err != nil {
				require.ErrorIs(t, err, io.EOF)
				break
			}
			if r.Event != nil && r.Event.Method == "fixture/burst" {
				var p struct{ Sequence int }
				require.NoError(t, json.Unmarshal(r.Params, &p))
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
		_, e = s.Submit(canceled, codex.Input{})
		var notSent *codex.CallError
		require.ErrorAs(t, e, &notSent)
		assert.Equal(t, codex.NotSent, notSent.Dispatch)
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		go func() { _, err := s.Submit(waiting, codex.Input{}); result <- err }()
		codexUntil(t, s, "fixture/submitted")
		stop()
		e = <-result
		var unknown *codex.CallError
		require.ErrorAs(t, e, &unknown)
		assert.Equal(t, codex.OutcomeUnknown, unknown.Dispatch)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "turn/start"))
		assert.NoError(t, s.Err())
		canceled, cancel = context.WithCancel(ctx)
		cancel()
		_, e = s.Receive(canceled)
		assert.ErrorIs(t, e, context.Canceled)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
	case "session-resume":
		other, err := codex.Open(ctx, codex.SessionOptions{Client: c})
		require.NoError(t, err)
		defer other.Close()
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit-main-turn"}))
		codexUntil(t, s, "fixture/interrupt")
		select {
		case <-s.Done():
			t.Fatal("Interrupt closed Session")
		default:
		}
		require.NoError(t, s.Close())
		testsupport.Wait(t, ctx, s.Done())
		_, e = other.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		codexUntil(t, other, "fixture/submitted")
		assert.NoError(t, other.Err())
		require.Error(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "late"}))
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "thread/unsubscribe"))
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "turn/interrupt"))
		var targets []string
		for _, b := range testsupport.PeerFrames(t, path) {
			var f struct {
				Method string
				Params struct{ ThreadID, TurnID string }
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Method == "turn/interrupt" {
				targets = append(targets, f.Params.ThreadID+":"+f.Params.TurnID)
			}
		}
		assert.Equal(t, []string{"root:explicit-main-turn"}, targets)
	case "session-error-result":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		requests := codextest.Requests(t, s, 1)
		all := codexUntil(t, s, "turn/completed")
		terminal := all[len(all)-1].Event.Data.(*ct.TurnCompletedNotification)
		assert.Equal(t, ct.TurnStatusFailed, terminal.Turn.Status)
		assert.NoError(t, s.Err())
		require.Error(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "disconnect"}))
		testsupport.Wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		require.Error(t, requests[0].Approve(ctx, codex.ApprovalOptions{}))
		drained := 0
		for {
			r, err := s.Receive(ctx)
			if err != nil {
				require.ErrorIs(t, err, cause)
				break
			}
			if r.Event != nil && r.Event.Method == "fixture/burst" {
				var p struct{ Sequence int }
				require.NoError(t, json.Unmarshal(r.Params, &p))
				assert.Equal(t, drained, p.Sequence, "accepted facts drain in order")
				drained++
			}
		}
		assert.Equal(t, 32, drained)
		_ = s.Close()
		assert.Equal(t, cause, s.Err())
	case "session-close":
		_, e = s.Submit(ctx, codex.Input{})
		_ = e
		testsupport.Wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		all := codexDrain(t, s, cause)
		require.NotEmpty(t, all)
		assert.Equal(t, "fixture/submitted", all[0].Event.Method)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "initialize"))
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "thread/start"))
		_, e = s.Submit(ctx, codex.Input{})
		require.Error(t, e)
		assert.Equal(t, 1, testsupport.CountFrames(t, path, "turn/start"))
	default:
		t.Fatalf("missing native acceptance for %s", scenario)
	}
}

func codexDrain(t *testing.T, s *codex.Session, cause error) []codex.Received {
	t.Helper()
	var all []codex.Received
	ctx := testsupport.Deadline(t)
	for {
		r, e := s.Receive(ctx)
		if e != nil {
			require.ErrorIs(t, e, cause)
			return all
		}
		all = append(all, r)
	}
}
