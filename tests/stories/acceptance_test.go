package stories_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
	clt "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/codex"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
)

// storyObservation is a detached test fact extracted by a native adapter. Raw keeps
// the observed wire payload; it is never used to construct response authority.
type storyObservation struct{ Kind, ID, Parent, Raw string }
type storyTrace struct {
	Events                                                                                       []storyObservation
	SendError, AnswerError, RetryError, CanceledError, OverflowError, InterruptError, CloseError error
	AnswerID, AnswerText, AnswerDecision                                                         string
	AnswerCount, InputCount                                                                      int
}

var storyIDs = []string{"US-001", "US-002", "US-003", "US-004", "US-005", "US-006", "US-007", "US-008", "US-009", "US-010", "US-011", "US-012"}

// checkStory applies common user-visible acceptance rules, without requiring native
// events to share a schema, an ordering beyond causal boundaries, or dedup rules.
func (s *CommonStoriesSuite) checkStory(story string, got storyTrace) {
	s.T().Helper()
	check := s.Assert()
	find := func(kind, id string) int {
		for i, e := range got.Events {
			if e.Kind == kind && (id == "" || e.ID == id) {
				return i
			}
		}
		return -1
	}
	switch story {
	case "US-001":
		check.NoError(got.SendError, "input submission failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "input barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "send acknowledgement confused with task completion")
	case "US-002":
		check.GreaterOrEqual(find("tool", ""), 0, "tool completion missing")
		check.Greater(find("complete", "root"), find("tool", ""), "tool completion must remain separate from task completion")
	case "US-003":
		found := false
		for _, e := range got.Events {
			if e.Kind == "associated" && e.ID == "child" && e.Parent == "root" {
				found = true
			}
		}
		check.True(found, "confirmed child parent lost")
		check.GreaterOrEqual(find("native", "child"), 0, "child native source lost")
	case "US-004":
		for _, pair := range [][2]string{{"child", "root"}, {"sibling", "root"}, {"grand", "child"}} {
			found := false
			for _, e := range got.Events {
				if e.Kind == "associated" && e.ID == pair[0] && e.Parent == pair[1] {
					found = true
				}
			}
			check.True(found, "nested/parallel relation lost: "+pair[0])
		}
		check.GreaterOrEqual(find("gap", ""), 0, "unknown source must produce visible uncertainty")
		check.Less(find("associated", "unknown"), 0, "unknown parent guessed")
	case "US-005":
		check.GreaterOrEqual(find("complete", "root"), 0, "root completion missing")
		check.Greater(find("native", "child"), find("complete", "root"), "child observation ended at root completion")
	case "US-006", "US-006-allow", "US-007":
		check.NoError(got.AnswerError, "original handle first answer")
		check.Error(got.RetryError, "original handle must be single use")
		check.Equal(1, got.AnswerCount, "answer uniqueness lost")
		check.Equal("original", got.AnswerID, "answer correlation lost")
		if story == "US-006" {
			check.Equal("deny", got.AnswerDecision, "native permission refusal changed")
		}
		if story == "US-006-allow" {
			check.Equal("allow", got.AnswerDecision, "native permission approval changed")
		}
		if story == "US-007" {
			check.Equal("free text / 自由文本", got.AnswerText, "answer not mapped to original question")
		}
	case "US-008":
		check.ErrorIs(got.CanceledError, context.Canceled, "canceled answer must retain native context cancellation")
		check.NoError(got.AnswerError, "pre-canceled answer must leave original handle available under native contract")
		check.Equal(1, got.AnswerCount, "pre-canceled answer must not consume handle")
	case "US-008-close":
		check.NoError(got.CloseError, "close failed")
		check.Error(got.AnswerError, "closing while waiting for an answer must invalidate the original handle")
		check.Error(got.RetryError, "closed original handle must stay invalid")
	case "US-009":
		check.NoError(got.OverflowError, "slow consumer terminated observation")
		bursts := 0
		for _, event := range got.Events {
			if event.Kind == "burst" {
				bursts++
			}
		}
		check.Equal(32, bursts, "accepted burst must drain completely")
		check.Equal("barrier", got.Events[len(got.Events)-1].Kind, "burst barrier must remain last")
	case "US-010":
		unknown := 0
		for _, e := range got.Events {
			if e.Kind == "unknown" {
				unknown++
				check.Contains(e.Raw, `"future":{"big":9007199254740993,"nullable":null}`, "unknown payload changed")
			}
		}
		check.Equal(2, unknown, "unknown no-ID arrivals must survive independently")
	case "US-011":
		check.GreaterOrEqual(find("gap", ""), 0, "history recovery gap lost")
		check.GreaterOrEqual(find("associated", "child"), 0, "history association missing")
		check.Less(find("created", "child"), 0, "history association invented creation")
		check.Zero(got.InputCount, "recovery replayed input")
		check.Zero(got.AnswerCount, "recovery replayed answer")
	case "US-012":
		check.NoError(got.InterruptError, "interrupt failed")
		check.NoError(got.CloseError, "close failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "interrupt barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "interrupt acknowledgement fabricated completion")
	}
}

const claudeStoryID = "11111111-1111-4111-8111-111111111111"

func codexTargetOptions(t *testing.T, story string) (codex.StdioOptions, string) {
	t.Helper()
	exe, e := os.Executable()
	require.NoError(t, e)
	path := filepath.Join(t.TempDir(), "wire.jsonl")
	return codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_STORY_PEER=codex", "SDK_STORY=" + story, "SDK_STORY_TRACE=" + path, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "session-stories", Version: "synthetic"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}}, path
}
func claudeTargetOptions(t *testing.T, story string) (claude.SessionOptions, string) {
	t.Helper()
	exe, e := os.Executable()
	require.NoError(t, e)
	dir := t.TempDir()
	dir, e = filepath.EvalSymlinks(dir)
	require.NoError(t, e)
	path := filepath.Join(dir, "wire.jsonl")
	return claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "CLAUDE_CONFIG_DIR=" + filepath.Join(dir, "config"), "SDK_STORY_PEER=claude", "SDK_STORY=" + story, "SDK_STORY_TRACE=" + path, "GORACE=atexit_sleep_ms=0"}}, path
}
func codexUntil(t *testing.T, s *codex.Session, method string) []codex.Received {
	t.Helper()
	ctx := deadline(t)
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
func claudeUntil(t *testing.T, s *claude.Session, subtype string) []claude.Received {
	t.Helper()
	ctx := deadline(t)
	var all []claude.Received
	for {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		all = append(all, r)
		if r.Event != nil && (r.Event.Subtype == subtype || fixtureKind(r.Raw) == subtype || subtype == "fixture_tool" && r.Event.Type == "user") {
			return all
		}
	}
}
func codexSessionTarget(t *testing.T, story string) {
	o, path := codexTargetOptions(t, story)
	c, e := codex.NewStdio(deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = c.Close() })
	creation := ct.ThreadStartParams{}
	if story == "SDK-US-012" || story == "SDK-US-013" {
		creation.Model = ct.Some("creation-model")
		creation.Config = ct.Some(map[string]json.RawMessage{"model_reasoning_effort": json.RawMessage(`"medium"`)})
	}
	s, e := codex.Open(deadline(t), codex.SessionOptions{Client: c, MaxParentDepth: 1, Thread: creation})
	require.NoError(t, e)
	t.Cleanup(func() { _ = s.Close() })
	ctx := deadline(t)
	switch story {
	case "SDK-US-001":
		_, e = codex.Open(ctx, codex.SessionOptions{Client: c})
		require.ErrorIs(t, e, codex.ErrOwnershipConflict)
		for range 2 {
			v, e := s.Submit(ctx, codex.Input{})
			require.NoError(t, e)
			require.Equal(t, "turn", v.Response.Turn.ID)
			got := codexUntil(t, s, "turn/completed")
			require.Equal(t, "live", got[len(got)-1].Origin)
			select {
			case <-s.Done():
				t.Fatal("result ended Session")
			default:
			}
		}
	case "SDK-US-002":
		var input ct.UserInput
		require.NoError(t, json.Unmarshal([]byte(`{"type":"image","url":"https://fixture.invalid/image.png"}`), &input))
		_, e = s.Submit(ctx, codex.Input{Input: []ct.UserInput{input}, Model: ct.Some("native-model"), OutputSchema: ct.Some(json.RawMessage(`{"type":"object","x":null}`))})
		require.NoError(t, e)
		r := codexUntil(t, s, "fixture/submitted")
		wire := observedWire(t, r[len(r)-1].Params)
		assert.Equal(t, "root", wire["threadId"])
		assert.Equal(t, "native-model", wire["model"])
		assert.JSONEq(t, `[{"type":"image","url":"https://fixture.invalid/image.png"}]`, string(marshal(t, wire["input"])))
		assert.JSONEq(t, `{"type":"object","x":null}`, string(marshal(t, wire["outputSchema"])))
		_, e = s.Submit(ctx, codex.Input{Input: []ct.UserInput{{Unknown: json.RawMessage(`{"type":"future"}`)}}})
		require.ErrorIs(t, e, codex.ErrInvalidInput)
		require.Equal(t, 1, countFrames(t, path, "turn/start"))
	case "SDK-US-003":
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
		require.Equal(t, 3, countFrames(t, path, "turn/start"))
	case "SDK-US-004":
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
	case "SDK-US-005":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		all := codexUntil(t, s, "fixture/barrier")
		var raw []string
		for _, r := range all {
			if r.Event != nil && r.Event.Method == "future/repeated" {
				require.Equal(t, "live", r.Origin)
				require.Nil(t, r.Event.History)
				require.Contains(t, string(r.Raw), `"n":9007199254740993`)
				raw = append(raw, string(r.Raw))
			}
		}
		require.Len(t, raw, 2)
		assert.Equal(t, raw[0], raw[1])

	case "SDK-US-006", "SDK-US-007":
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
		require.Zero(t, countFrames(t, path, "thread/resume"))
		require.Zero(t, countFrames(t, path, "thread/read"))
	case "SDK-US-008":
		page, e := s.ListSubagents(ctx, codex.SubagentQuery{MaxPages: 1, Limit: ct.Some(uint32(1))})
		require.ErrorIs(t, e, codex.ErrLimit)
		require.Len(t, page.Pages, 1)
		assert.False(t, page.Coverage.EnumerationComplete)
		assert.Equal(t, "p2", page.NextCursor.ValueOr(""))
		full, e := s.ListSubagents(ctx, codex.SubagentQuery{Limit: ct.Some(uint32(1))})
		require.NoError(t, e)
		require.Len(t, full.Pages, 2)
		assert.True(t, full.Coverage.EnumerationComplete)
		assert.Equal(t, "stored-a", full.Pages[0].Data[0].ID)
		assert.False(t, full.Coverage.Archived)
		archived, e := s.ListSubagents(ctx, codex.SubagentQuery{Archived: ct.Some(true)})
		require.NoError(t, e)
		assert.True(t, archived.Coverage.Archived)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		all := codexUntil(t, s, "fixture/interrupt")
		require.Len(t, all, 1)
		for _, b := range peerFrames(t, path) {
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
	case "SDK-US-009", "SDK-US-010", "SDK-US-011":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		requests := codexRequests(t, s, 2)
		require.Contains(t, string(requests[0].Raw()), "future_envelope")
		require.Contains(t, string(requests[0].Params()), `"future":42`)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		assert.Zero(t, countAnswers(t, path, "codex"), "unanswered request was automatically decided")
		if story == "SDK-US-010" {
			wait(t, ctx, requests[0].Context().Done())
			require.Error(t, requests[0].Deny(ctx, codex.ApprovalOptions{}))
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, requests[1].Approve(canceled, codex.ApprovalOptions{}), context.Canceled)
			require.NoError(t, requests[1].Approve(ctx, codex.ApprovalOptions{}))
			require.Error(t, requests[1].Approve(ctx, codex.ApprovalOptions{}))
		} else if story == "SDK-US-011" {
			for i, r := range requests {
				answer := []string{"A"}
				if i == 1 {
					answer = []string{"free text / 自由文本"}
				}
				require.ErrorIs(t, r.Approve(ctx, codex.ApprovalOptions{}), codex.ErrInvalidInput)
				require.NoError(t, r.Answer(ctx, ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{"q": {Answers: answer}}}))
				require.Error(t, r.Answer(ctx, ct.ToolRequestUserInputResponse{Answers: map[string]ct.ToolRequestUserInputAnswer{"q": {Answers: answer}}}))
			}
		} else {
			require.NoError(t, requests[0].Deny(ctx, codex.ApprovalOptions{}))
			var ext ct.CommandExecutionRequestApprovalResponse
			require.NoError(t, json.Unmarshal([]byte(`{"decision":{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["echo","fixture"]}}}`), &ext))
			require.NoError(t, requests[1].Approve(ctx, codex.ApprovalOptions{CommandExecution: &ext}))
			require.Error(t, requests[1].Deny(ctx, codex.ApprovalOptions{}))
		}
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		n := 2
		if story == "SDK-US-010" {
			n = 1
		}
		assert.Equal(t, n, countAnswers(t, path, "codex"))
		require.Equal(t, 1, countFrames(t, path, "turn/start"))
		checkAnswerWires(t, path, story, "codex")

	case "SDK-US-012", "SDK-US-013":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		codexUntil(t, s, "fixture/submitted")
		receipt, e := s.SetModel(ctx, "requested-model")
		require.NoError(t, e)
		require.True(t, receipt.NativeAccepted)
		assert.Equal(t, "requested-model", receipt.Requested.Model.ValueOr(""))
		assert.Equal(t, "root", receipt.Requested.ThreadID)
		effort := ct.ReasoningEffort("high")
		thinking, e := s.SetThinking(ctx, codex.ThinkingChange{Effort: ct.Some(effort)})
		require.NoError(t, e)
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
		for _, b := range peerFrames(t, path) {
			var f struct {
				Method string
				Params map[string]json.RawMessage
			}
			require.NoError(t, json.Unmarshal(b, &f))
			if f.Method == "thread/settings/update" {
				assert.JSONEq(t, `"root"`, string(f.Params["threadId"]))
			}
		}
		assert.Equal(t, 1, countFrames(t, path, "thread/start"))
		assert.Equal(t, 3, countFrames(t, path, "thread/settings/update"))
		var starts, settings []json.RawMessage
		for _, b := range peerFrames(t, path) {
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
	case "SDK-US-014":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"})) // all 256 frames are unread
		require.NoError(t, s.Close())
		wait(t, ctx, s.Done())
		var sequence []int
		for {
			r, e := s.Receive(ctx)
			if e != nil {
				require.ErrorIs(t, e, io.EOF)
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
	case "SDK-US-015":
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, e = s.Submit(canceled, codex.Input{})
		var notSent *codex.CallError
		require.ErrorAs(t, e, &notSent)
		assert.Equal(t, codex.NotSent, notSent.Dispatch)
		waiting, stop := context.WithCancel(ctx)
		defer stop()
		result := make(chan error, 1)
		go func() { _, e := s.Submit(waiting, codex.Input{}); result <- e }()
		codexUntil(t, s, "fixture/submitted")
		stop()
		e = <-result
		var unknown *codex.CallError
		require.ErrorAs(t, e, &unknown)
		assert.Equal(t, codex.OutcomeUnknown, unknown.Dispatch)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
		assert.Equal(t, 1, countFrames(t, path, "turn/start"))
		assert.NoError(t, s.Err())
		canceled, cancel = context.WithCancel(ctx)
		cancel()
		_, e = s.Receive(canceled)
		assert.ErrorIs(t, e, context.Canceled)
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit"}))
		codexUntil(t, s, "fixture/interrupt")
	case "SDK-US-016":
		other, e := codex.Open(ctx, codex.SessionOptions{Client: c})
		require.NoError(t, e)
		defer other.Close()
		require.NoError(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "explicit-main-turn"}))
		codexUntil(t, s, "fixture/interrupt")
		select {
		case <-s.Done():
			t.Fatal("Interrupt closed Session")
		default:
		}
		require.NoError(t, s.Close())
		wait(t, ctx, s.Done())
		_, e = other.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		codexUntil(t, other, "fixture/submitted")
		assert.NoError(t, other.Err())
		require.Error(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "late"}))
		assert.Equal(t, 1, countFrames(t, path, "thread/unsubscribe"))
		assert.Equal(t, 1, countFrames(t, path, "turn/interrupt"))
		var targets []string
		for _, b := range peerFrames(t, path) {
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
	case "SDK-US-017":
		_, e = s.Submit(ctx, codex.Input{})
		require.NoError(t, e)
		requests := codexRequests(t, s, 1)
		all := codexUntil(t, s, "turn/completed")
		terminal := all[len(all)-1].Event.Data.(*ct.TurnCompletedNotification)
		assert.Equal(t, ct.TurnStatusFailed, terminal.Turn.Status)
		assert.NoError(t, s.Err())
		require.Error(t, s.Interrupt(ctx, codex.InterruptOptions{TurnID: "disconnect"}))
		wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		require.Error(t, requests[0].Approve(ctx, codex.ApprovalOptions{}))
		drained := 0
		for {
			r, e := s.Receive(ctx)
			if e != nil {
				require.ErrorIs(t, e, cause)
				break
			}
			if r.Event != nil && r.Event.Method == "fixture/burst" {
				drained++
			}
		}
		assert.Equal(t, 32, drained)
		_ = s.Close()
		assert.Equal(t, cause, s.Err())
	case "SDK-US-018":
		_, e = s.Submit(ctx, codex.Input{})
		_ = e
		wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		all := codexDrain(t, s, cause)
		require.NotEmpty(t, all)
		assert.Equal(t, "fixture/submitted", all[0].Event.Method)
		assert.Equal(t, 1, countFrames(t, path, "initialize"))
		assert.Equal(t, 1, countFrames(t, path, "thread/start"))
		_, e = s.Submit(ctx, codex.Input{})
		require.Error(t, e)
		assert.Equal(t, 1, countFrames(t, path, "turn/start"))
	default:
		t.Fatalf("missing native acceptance for %s", story)
	}
}
func claudeSessionTarget(t *testing.T, story string) {
	o, path := claudeTargetOptions(t, story)
	if story == "SDK-US-012" || story == "SDK-US-013" {
		budget := 1024
		display := "summarized"
		o.Model = "creation-model"
		o.Thinking = &clt.ThinkingConfig{Type: "enabled", BudgetTokens: &budget, Display: &display}
	}
	s, e := claude.Open(deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = s.Close() })
	ctx := deadline(t)
	claudeUntil(t, s, "init")
	switch story {
	case "SDK-US-001":
		for range 2 {
			v, e := s.Submit(ctx, claude.Input{Text: "next"})
			require.NoError(t, e)
			require.True(t, v.Written)
			got := claudeUntil(t, s, "success")
			require.Equal(t, "result", got[len(got)-1].Event.Type)
			select {
			case <-s.Done():
				t.Fatal("result ended Session")
			default:
			}
		}
	case "SDK-US-002":
		var u clt.UserInput
		require.NoError(t, json.Unmarshal([]byte(`{"message":{"role":"user","content":[{"type":"text","text":"exact"},{"type":"image","source":{"type":"url","url":"https://fixture.invalid/i"},"future":null},{"type":"document","source":{"type":"text","data":"doc","media_type":"text/plain"}},{"type":"tool_result","tool_use_id":"original","content":"native"}]},"priority":"later","future":{"n":9007199254740993}}`), &u))
		_, e = s.Submit(ctx, claude.Input{User: &u})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_submitted")
		wire := observedWire(t, all[len(all)-1].Raw)
		assert.Equal(t, "", wire["session_id"])
		assert.Nil(t, wire["parent_tool_use_id"])
		assert.Equal(t, "later", wire["priority"])
		assert.JSONEq(t, `{"role":"user","content":[{"type":"text","text":"exact"},{"type":"image","source":{"type":"url","url":"https://fixture.invalid/i"},"future":null},{"type":"document","source":{"type":"text","data":"doc","media_type":"text/plain"}},{"type":"tool_result","tool_use_id":"original","content":"native"}]}`, string(marshal(t, wire["message"])))
		assert.JSONEq(t, `{"n":9007199254740993}`, string(marshal(t, wire["future"])))
		assert.Contains(t, string(all[len(all)-1].Raw), `9007199254740993`)
		require.NoError(t, json.Unmarshal([]byte(`{"session_id":"child","message":{"role":"user","content":"forbidden"}}`), &u))
		_, e = s.Submit(ctx, claude.Input{User: &u})
		require.ErrorIs(t, e, claude.ErrInvalidInput)
		require.Equal(t, 1, countFrames(t, path, "user"))
	case "SDK-US-003":
		// Native stream-json gives no submit acceptance RPC. Continue writing while
		// the first execution has no result; a native failure remains a received fact.
		for range 2 {
			v, e := s.Submit(ctx, claude.Input{Text: "supplement"})
			require.NoError(t, e)
			require.True(t, v.Written)
			claudeUntil(t, s, "fixture_submitted")
		}
		all := claudeUntil(t, s, "success")
		require.True(t, all[len(all)-1].Event.Result.IsError)
		assert.NoError(t, s.Err())
		require.Equal(t, 2, countFrames(t, path, "user"))
	case "SDK-US-004":
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
	case "SDK-US-005":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		all := claudeUntil(t, s, "fixture_barrier")
		var raw []string
		for _, r := range all {
			if r.Event != nil && fixtureKind(r.Raw) == "future_repeated" {
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

	case "SDK-US-006", "SDK-US-007":
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
			if r.Observation != nil && (r.Observation.Reason == "forwarding-not-confirmed" || r.Observation.Reason == "identity-extension-unverified") {
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
		require.Equal(t, 1, countFrames(t, path, "user"))
	case "SDK-US-008":
		dir := prepareClaudeRecords(t, o)
		page, e := s.ListSubagents(ctx, claude.SubagentQuery{MaxRecords: 1})
		require.ErrorIs(t, e, claude.ErrDiscoveryCoverage)
		require.Len(t, page.Records, 1)
		assert.Equal(t, "partial", page.Coverage.Status)
		full, e := s.ListSubagents(ctx, claude.SubagentQuery{})
		require.NoError(t, e)
		require.Len(t, full.Records, 2)
		assert.Equal(t, "complete", full.Coverage.Status)
		assert.False(t, full.Coverage.Atomic)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-new.jsonl"), []byte(`{"type":"assistant","message":{"content":"stored"}}`+"\n"), 0600))
		fresh, e := s.ListSubagents(ctx, claude.SubagentQuery{})
		require.NoError(t, e)
		require.Len(t, fresh.Records, 3)
		for _, r := range fresh.Records {
			assert.Equal(t, "unknown", r.Running)
		}
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		all := claudeUntil(t, s, "fixture_interrupt")
		require.Len(t, all, 1)
		require.Zero(t, countFrames(t, path, "user"))
		assert.Zero(t, countAnswers(t, path, "claude"))
	case "SDK-US-009", "SDK-US-010", "SDK-US-011":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		requests := claudeRequests(t, s, 2)
		require.Contains(t, string(requests[0].Raw()), "future_envelope")
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		assert.Zero(t, countAnswers(t, path, "claude"))
		if story == "SDK-US-010" {
			wait(t, ctx, requests[0].Context().Done())
			require.Error(t, requests[0].Deny(ctx, claude.DenialOptions{}))
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			require.ErrorIs(t, requests[1].Approve(canceled, claude.ApprovalOptions{}), context.Canceled)
			require.NoError(t, requests[1].Approve(ctx, claude.ApprovalOptions{}))
			require.Error(t, requests[1].Approve(ctx, claude.ApprovalOptions{}))
		} else if story == "SDK-US-011" {
			for i, r := range requests {
				answer := "A"
				if i == 1 {
					answer = "free text / 自由文本"
				}
				require.NoError(t, r.Answer(ctx, claude.QuestionAnswers{Answers: map[string]string{"Choose?": answer}}))
				require.Error(t, r.Answer(ctx, claude.QuestionAnswers{Answers: map[string]string{"Choose?": answer}}))
			}
		} else {
			stop := true
			require.NoError(t, requests[0].Deny(ctx, claude.DenialOptions{Message: "user refused", Interrupt: &stop}))
			require.NoError(t, requests[1].Approve(ctx, claude.ApprovalOptions{UpdatedInput: json.RawMessage(`{"command":"echo modified","future":42}`), UpdatedPermissions: []clt.PermissionUpdate{{Type: "setMode", Mode: "acceptEdits", Destination: "session"}}}))
			require.Error(t, requests[1].Deny(ctx, claude.DenialOptions{}))
		}
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		claudeUntil(t, s, "fixture_interrupt")
		n := 2
		if story == "SDK-US-010" {
			n = 1
		}
		assert.Equal(t, n, countAnswers(t, path, "claude"))
		require.Equal(t, 1, countFrames(t, path, "user"))
		checkAnswerWires(t, path, story, "claude")

	case "SDK-US-012", "SDK-US-013":
		_, e = s.Submit(ctx, claude.Input{Text: "executing"})
		require.NoError(t, e)
		claudeUntil(t, s, "fixture_submitted")
		model := "requested-model"
		receipt, e := s.SetModel(ctx, claude.ModelChange{Model: &model})
		require.NoError(t, e)
		require.True(t, receipt.Accepted)
		assert.Nil(t, receipt.Observed)
		assert.JSONEq(t, `{"subtype":"set_model","model":"requested-model"}`, string(receipt.Requested))
		assert.Contains(t, string(receipt.Response), `"success"`)
		zero := 0
		display := "omitted"
		thinking, e := s.SetThinking(ctx, claude.ThinkingChange{MaxThinkingTokens: &zero, ThinkingDisplay: &display})
		require.NoError(t, e)
		assert.True(t, thinking.Accepted)
		assert.Nil(t, thinking.Observed)
		assert.JSONEq(t, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":0,"thinking_display":"omitted"}`, string(thinking.Requested))
		cleared, e := s.SetThinking(ctx, claude.ThinkingChange{ClearDisplay: true})
		require.NoError(t, e)
		assert.JSONEq(t, `{"subtype":"set_max_thinking_tokens","max_thinking_tokens":null,"thinking_display":null}`, string(cleared.Requested))
		def, e := s.SetModel(ctx, claude.ModelChange{})
		require.NoError(t, e)
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
		assert.Equal(t, 1, countFrames(t, path, "user"))
		assert.Equal(t, 1, countFrames(t, path, "launch"))
		frames := peerFrames(t, path)
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
	case "SDK-US-014":
		_, e = s.Submit(ctx, claude.Input{Text: "burst"})
		require.NoError(t, e)
		require.NoError(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		require.NoError(t, s.Close())
		wait(t, ctx, s.Done())
		var sequence []int
		for {
			r, e := s.Receive(ctx)
			if e != nil {
				require.ErrorIs(t, e, io.EOF)
				break
			}
			if r.Event != nil && fixtureKind(r.Raw) == "fixture_burst" {
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
	case "SDK-US-015":
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
		go func() { _, e := s.Submit(waiting, claude.Input{Text: strings.Repeat("x", 2<<20)}); result <- e }()
		require.Eventually(t, func() bool { _, err := os.Stat(path + ".prefix"); return err == nil }, time.Second, time.Millisecond)
		stop()
		e = <-result
		var unknown *claude.DispatchError
		require.ErrorAs(t, e, &unknown)
		assert.Equal(t, claude.OutcomeUnknown, unknown.State)
		require.ErrorIs(t, e, claude.ErrUnknownOutcome)
		require.NoError(t, os.WriteFile(path+".release", nil, 0600))
		require.ErrorIs(t, s.Close(), claude.ErrUnknownOutcome)
		assert.Equal(t, 1, countFrames(t, path, "user"))
		// Local Receive cancellation was checked before dispatch, while Session is live.
	case "SDK-US-016":
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
		wait(t, ctx, s.Done())
		require.NoError(t, s.Close())
		require.Error(t, s.Interrupt(ctx, claude.InterruptOptions{}))
		assert.Equal(t, 1, countFrames(t, path, "launch"))
	case "SDK-US-017":
		_, e = s.Submit(ctx, claude.Input{Text: "start"})
		require.NoError(t, e)
		requests := claudeRequests(t, s, 1)
		all := claudeUntil(t, s, "success")
		assert.True(t, all[len(all)-1].Event.Result.IsError)
		assert.NoError(t, s.Err())
		_ = s.Interrupt(ctx, claude.InterruptOptions{})
		wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		require.Error(t, requests[0].Approve(ctx, claude.ApprovalOptions{}))
		drained := 0
		for {
			r, e := s.Receive(ctx)
			if e != nil {
				require.ErrorIs(t, e, cause)
				break
			}
			if r.Event != nil && fixtureKind(r.Raw) == "fixture_burst" {
				var payload struct {
					Sequence int `json:"sequence"`
				}
				require.NoError(t, json.Unmarshal(r.Raw, &payload))
				assert.Equal(t, drained, payload.Sequence, "accepted native facts must drain in order")
				drained++
			}
		}
		assert.Equal(t, 32, drained)
		_ = s.Close()
		assert.Equal(t, cause, s.Err())
	case "SDK-US-018":
		_, e = s.Submit(ctx, claude.Input{Text: "disconnect"})
		require.NoError(t, e)
		wait(t, ctx, s.Done())
		cause := s.Err()
		require.Error(t, cause)
		drained := 0
		for {
			r, e := s.Receive(ctx)
			if e != nil {
				require.ErrorIs(t, e, cause)
				break
			}
			if r.Event != nil {
				drained++
				assert.Equal(t, "fixture_submitted", fixtureKind(r.Raw))
			}
		}
		assert.Equal(t, 1, drained)
		assert.Equal(t, 1, countFrames(t, path, "launch"))
		_, e = s.Submit(ctx, claude.Input{Text: "no retry"})
		require.Error(t, e)
		assert.Equal(t, 1, countFrames(t, path, "user"))
	default:
		t.Fatalf("missing native acceptance for %s", story)
	}
}
func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	require.NoError(t, e)
	return b
}
func observedWire(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &envelope))
	wire := envelope["wire"]
	var f map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &f))
	if p, ok := f["params"]; ok {
		wire = p
	}
	var result map[string]any
	d := json.NewDecoder(strings.NewReader(string(wire)))
	d.UseNumber()
	require.NoError(t, d.Decode(&result))
	return result
}
func countFrames(t *testing.T, path, method string) int {
	t.Helper()
	n := 0
	for _, b := range peerFrames(t, path) {
		var f struct{ Method, Type string }
		require.NoError(t, json.Unmarshal(b, &f))
		if f.Method == method || f.Type == method {
			n++
		}
	}
	return n
}

// Trace reads only bytes the native peer actually received. Assertions never read
// private Session membership, queue length, or permission registry state.
func peerFrames(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	data, e := os.ReadFile(path)
	require.NoError(t, e)
	var frames []json.RawMessage
	for _, line := range splitLines(data) {
		frames = append(frames, json.RawMessage(line))
	}
	return frames
}
func splitLines(b []byte) [][]byte {
	var result [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				result = append(result, b[start:i])
			}
			start = i + 1
		}
	}
	return result
}

func fixtureKind(b []byte) string {
	var x struct{ Fixture string }
	_ = json.Unmarshal(b, &x)
	return x.Fixture
}

func codexRequests(t *testing.T, s *codex.Session, n int) []*codex.IncomingRequest {
	t.Helper()
	var all []*codex.IncomingRequest
	ctx := deadline(t)
	for len(all) < n {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		if r.Request != nil {
			_, err := r.Request.Data()
			require.NoError(t, err)
			all = append(all, r.Request)
		}
	}
	return all
}
func claudeRequests(t *testing.T, s *claude.Session, n int) []*claude.IncomingRequest {
	t.Helper()
	var all []*claude.IncomingRequest
	ctx := deadline(t)
	for len(all) < n {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		if r.Request != nil {
			all = append(all, r.Request)
		}
	}
	return all
}
func countAnswers(t *testing.T, path, provider string) int {
	t.Helper()
	n := 0
	for _, b := range peerFrames(t, path) {
		var f struct {
			ID           json.RawMessage
			Method, Type string
		}
		require.NoError(t, json.Unmarshal(b, &f))
		if provider == "codex" && len(f.ID) > 0 && f.Method == "" || provider == "claude" && f.Type == "control_response" {
			n++
		}
	}
	return n
}
func checkAnswerWires(t *testing.T, path, story, provider string) {
	t.Helper()
	var answers []string
	for _, b := range peerFrames(t, path) {
		var f struct {
			ID           json.RawMessage
			Method, Type string
		}
		require.NoError(t, json.Unmarshal(b, &f))
		if provider == "codex" && len(f.ID) > 0 && f.Method == "" || provider == "claude" && f.Type == "control_response" {
			answers = append(answers, string(b))
		}
	}
	require.NotEmpty(t, answers)
	for i, b := range answers {
		var f struct {
			ID       string
			Response struct {
				RequestID string `json:"request_id"`
			}
		}
		require.NoError(t, json.Unmarshal([]byte(b), &f))
		want := "first"
		if i == 1 || story == "SDK-US-010" {
			want = "second"
		}
		if provider == "codex" {
			assert.Equal(t, want, f.ID)
		} else {
			assert.Equal(t, want, f.Response.RequestID)
		}
	}

	if story == "SDK-US-009" {
		if provider == "codex" {
			assert.Contains(t, answers[0], `"decline"`)
			assert.Contains(t, answers[1], `"execpolicy_amendment":["echo","fixture"]`)
		} else {
			assert.Contains(t, answers[0], `"message":"user refused"`)
			assert.Contains(t, answers[0], `"interrupt":true`)
			assert.Contains(t, answers[1], `"updatedPermissions"`)
			assert.Contains(t, answers[1], `echo modified`)
		}
	}
	if story == "SDK-US-011" {
		assert.Contains(t, answers[0], `A`)
		assert.Contains(t, answers[1], `free text / 自由文本`)
		if provider == "codex" {
			var first, second struct {
				Result struct {
					Answers map[string]struct{ Answers []string }
				}
			}
			require.NoError(t, json.Unmarshal([]byte(answers[0]), &first))
			require.NoError(t, json.Unmarshal([]byte(answers[1]), &second))
			assert.Equal(t, []string{"A"}, first.Result.Answers["q"].Answers)
			assert.Equal(t, []string{"free text / 自由文本"}, second.Result.Answers["q"].Answers)
		} else {
			var w struct {
				Response struct {
					Response struct {
						UpdatedInput struct{ Answers map[string]string } `json:"updatedInput"`
					}
				}
			}
			require.NoError(t, json.Unmarshal([]byte(answers[1]), &w))
			assert.Equal(t, "free text / 自由文本", w.Response.Response.UpdatedInput.Answers["Choose?"])
		}
		if provider == "claude" {
			assert.Contains(t, answers[1], `"questions"`)
			assert.Contains(t, answers[1], `"future":42`)
		}
	}
}
func prepareClaudeRecords(t *testing.T, o claude.SessionOptions) string {
	t.Helper()
	config := ""
	for _, v := range o.Env {
		if strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") {
			config = strings.TrimPrefix(v, "CLAUDE_CONFIG_DIR=")
		}
	}
	key := regexp.MustCompile(`[^a-zA-Z0-9-]`).ReplaceAllString(o.WorkDir, "-")
	base := filepath.Join(config, "projects", key)
	sub := filepath.Join(base, claudeStoryID, "subagents")
	require.NoError(t, os.MkdirAll(filepath.Join(sub, "nested"), 0700))
	main := fmt.Sprintf(`{"type":"user","uuid":"u","parentUuid":null,"sessionId":%q,"cwd":%q,"message":{"role":"user","content":"stored question is not live authority"}}`, claudeStoryID, o.WorkDir)
	require.NoError(t, os.WriteFile(filepath.Join(base, claudeStoryID+".jsonl"), []byte(main+"\n"), 0600))
	for _, name := range []string{"agent-old.jsonl", "nested/agent-nested.jsonl"} {
		require.NoError(t, os.WriteFile(filepath.Join(sub, name), []byte(`{"type":"assistant","message":{"content":"retained"}}`+"\n"), 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested/agent-nested.meta.json"), []byte(`{"parentAgentId":"old","toolUseId":"spawn-nested","future":null}`), 0600))
	return sub
}

func codexDrain(t *testing.T, s *codex.Session, cause error) []codex.Received {
	t.Helper()
	var all []codex.Received
	ctx := deadline(t)
	for {
		r, e := s.Receive(ctx)
		if e != nil {
			require.ErrorIs(t, e, cause)
			return all
		}
		all = append(all, r)
	}
}
