package stories_test

import (
	"context"
	"crypto/sha256"
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

const querySession = "11111111-1111-4111-8111-111111111111"

func claudeQueryFixture(t *testing.T, source string) (claude.SessionQueryScope, string) {
	t.Helper()
	config := t.TempDir()
	path := filepath.Join(config, "projects", "-synthetic", querySession+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(source), 0400))
	return claude.SessionQueryScope{ConfigDir: config}, path
}

func queryLine(id, parent, kind, extra, message string) string {
	p := "null"
	if parent != "" {
		p = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"type":%q,"uuid":%q,"parentUuid":%s,"sessionId":%q,"timestamp":"2026-01-01T00:00:00Z",%s"message":%s}`+"\n", kind, id, p, querySession, extra, message)
}

func checkClaudeIndependentMessages(s *CommonStoriesSuite) {
	t := s.T()
	t.Setenv("PATH", t.TempDir()) // no runnable Claude runtime
	scope, path := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"hello","future":9007199254740993}`)+queryLine("a", "u", "assistant", "", `{"content":[{"type":"future","n":9007199254740993}]}`))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	page, err := claude.GetSessionMessages(context.Background(), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, "complete", page.Coverage.Status)
	require.Len(t, page.Messages, 2)
	require.Equal(t, "u", page.Messages[0].UUID)
	require.Equal(t, querySession, page.Messages[0].SessionID)
	require.Contains(t, string(page.Messages[1].Message), "9007199254740993")
	require.Contains(t, string(page.Messages[0].Raw), `"parentUuid":null`)
	page.Messages[0].Raw[0] = 'x'
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0400), stat.Mode().Perm())
}

// These cases enter through the native adapter, with expected message order
// taken from the pinned official chain/recovery algorithm, not live Events.
func checkClaudeMessageSelection(s *CommonStoriesSuite) {
	cases := []struct {
		name, source string
		want         []string
	}{
		{"branch", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("old", "u", "assistant", "", `{"content":"abandoned"}`) + queryLine("new", "u", "assistant", "", `{"content":"chosen"}`), []string{"u", "new"}},
		{"compaction", queryLine("old", "", "user", "", `{"content":"before"}`) + `{"type":"system","uuid":"boundary","parentUuid":null,"sessionId":"` + querySession + `","subtype":"compact_boundary","logicalParentUuid":"old"}` + "\n" + queryLine("summary", "boundary", "user", `"isCompactSummary":true,`, `{"content":"summary"}`) + queryLine("a", "summary", "assistant", "", `{"content":"after"}`), []string{"summary", "a"}},
		{"filtered", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("meta", "u", "user", `"isMeta":true,`, `{"content":"meta"}`) + queryLine("a", "meta", "assistant", "", `{"content":"answer"}`) + queryLine("side", "u", "assistant", `"isSidechain":true,`, `{"content":"side"}`) + queryLine("team", "u", "assistant", `"teamName":"x",`, `{"content":"team"}`), []string{"u", "a"}},
		{"parallel", queryLine("u", "", "user", "", `{"content":"root"}`) + queryLine("a1", "u", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t1"}]}`) + queryLine("a2", "a1", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t2"}]}`) + queryLine("r1", "a1", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) + queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) + queryLine("end", "r1", "assistant", "", `{"id":"end","content":"done"}`), []string{"u", "a1", "a2", "r2", "r1", "end"}},
		{"empty", "", []string{}},
		{"side-terminal-main-ancestor", queryLine("u", "", "user", "", `{"content":"main"}`) + queryLine("side", "u", "assistant", `"isSidechain":true,`, `{"content":"side"}`), []string{"u"}},
		{"only-side", queryLine("side", "", "user", `"isSidechain":true,`, `{"content":"side"}`), []string{}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			t := s.T()
			scope, _ := claudeQueryFixture(t, tc.source)
			page, err := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
			require.NoError(t, err)
			got := []string{}
			for _, m := range page.Messages {
				got = append(got, m.UUID)
			}
			require.Equal(t, tc.want, got)
			page, err = claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
			require.NoError(t, err)
			if len(tc.want) > 1 {
				require.Len(t, page.Messages, 1)
				require.Equal(t, tc.want[1], page.Messages[0].UUID)
			} else {
				require.Empty(t, page.Messages)
			}
			page, err = claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1000})
			require.NoError(t, err)
			require.Empty(t, page.Messages)
		})
	}
}

func checkClaudeMessageQueryFailures(s *CommonStoriesSuite) {
	valid := queryLine("u", "", "user", "", `{"content":"hello"}`)
	cases := []struct {
		name, source, kind string
		change             func(*claude.SessionMessagesOptions)
	}{
		{"malformed", "{\n", "corrupt", nil},
		{"duplicate-key", `{"type":"user","type":"assistant"}` + "\n", "corrupt", nil},
		{"duplicate-uuid", valid + valid, "corrupt", nil},
		{"cycle", queryLine("u", "a", "user", "", `{"content":"hello"}`) + queryLine("a", "u", "assistant", "", `{"content":"cycle"}`), "corrupt", nil},
		{"missing-parent", queryLine("u", "missing", "user", "", `{"content":"hello"}`), "corrupt", nil},
		{"unsupported", `{"type":"future"}` + "\n", "unsupported", nil},
		{"truncated", valid[:len(valid)-1], "corrupt", nil},
		{"metadata-identity", `{"type":"custom-title","sessionId":"another"}` + "\n", "corrupt", nil},
		{"negative-limit", valid, "invalid-input", func(o *claude.SessionMessagesOptions) { o.Limit = -1 }},
		{"negative-offset", valid, "invalid-input", func(o *claude.SessionMessagesOptions) { o.Offset = -1 }},
		{"bytes", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxBytes = 1 }},
		{"lines", valid + `{"type":"summary"}` + "\n", "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxLines = 1 }},
		{"index", valid + queryLine("a", "u", "assistant", "", `{"content":"answer"}`), "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxRecords = 1 }},
		{"line-size", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.MaxLineBytes = 10 }},
		{"return-limit", valid, "resource-limit", func(o *claude.SessionMessagesOptions) { o.Limit = 2; o.MaxMessages = 1 }},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			t := s.T()
			scope, _ := claudeQueryFixture(t, tc.source)
			o := claude.SessionMessagesOptions{Scope: scope}
			if tc.change != nil {
				tc.change(&o)
			}
			page, err := claude.GetSessionMessages(deadline(t), querySession, o)
			var qe *claude.SessionQueryError
			require.ErrorAs(t, err, &qe)
			require.Equal(t, tc.kind, qe.Kind)
			require.Empty(t, page.Messages)
			require.NotEqual(t, "complete", page.Coverage.Status)
		})
	}
}

func checkClaudeQueryScope(s *CommonStoriesSuite) {
	valid := queryLine("u", "", "user", "", `{"content":"hello"}`)
	s.Run("inaccessible", func() {
		t := s.T()
		scope, path := claudeQueryFixture(t, valid)
		scope.ConfigDir = path
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "inaccessible")
	})
	s.Run("missing", func() {
		scope, path := claudeQueryFixture(s.T(), valid)
		require.NoError(s.T(), os.Remove(path))
		p, e := claude.GetSessionMessages(deadline(s.T()), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(s.T(), p, e, "missing")
	})
	s.Run("ambiguous", func() {
		t := s.T()
		scope, path := claudeQueryFixture(t, valid)
		other := filepath.Join(scope.ConfigDir, "projects", "-other", filepath.Base(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(other), 0700))
		require.NoError(t, os.WriteFile(other, []byte(valid), 0400))
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "ambiguous")
	})
	s.Run("entry-limit", func() {
		t := s.T()
		scope, _ := claudeQueryFixture(t, valid)
		require.NoError(t, os.Mkdir(filepath.Join(scope.ConfigDir, "projects", "-other"), 0700))
		scope.MaxEntries = 1
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "resource-limit")
	})
	s.Run("symlink-file", func() {
		t := s.T()
		scope, path := claudeQueryFixture(t, valid)
		outside := filepath.Join(t.TempDir(), "private")
		require.NoError(t, os.WriteFile(outside, []byte(valid), 0400))
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(outside, path))
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "unsupported")
	})
	s.Run("symlink-project", func() {
		t := s.T()
		scope, path := claudeQueryFixture(t, valid)
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Remove(filepath.Dir(path)))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(path)))
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, p, e, "unsupported")
	})
	s.Run("invalid-input", func() {
		t := s.T()
		for _, id := range []string{"../session", "", "not-a-uuid"} {
			p, e := claude.GetSessionMessages(deadline(t), id, claude.SessionMessagesOptions{Scope: claude.SessionQueryScope{ConfigDir: t.TempDir()}})
			requireQueryKind(t, p, e, "invalid-input")
		}
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: claude.SessionQueryScope{ConfigDir: "relative"}})
		requireQueryKind(t, p, e, "invalid-input")
	})
	s.Run("canceled", func() {
		t := s.T()
		scope, _ := claudeQueryFixture(t, valid)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p, e := claude.GetSessionMessages(ctx, querySession, claude.SessionMessagesOptions{Scope: scope})
		require.ErrorIs(t, e, context.Canceled)
		require.Empty(t, p.Messages)
	})
	s.Run("explicit-project", func() {
		t := s.T()
		scope, path := claudeQueryFixture(t, valid)
		project := t.TempDir()
		scope.ProjectDir = project
		key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(project, "-")
		dest := filepath.Join(scope.ConfigDir, "projects", key)
		require.NoError(t, os.Rename(filepath.Dir(path), dest))
		p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		require.NoError(t, e)
		require.Len(t, p.Messages, 1)
	})
}

func requireQueryKind(t *testing.T, p claude.SessionMessagesPage, e error, kind string) {
	t.Helper()
	var qe *claude.SessionQueryError
	require.ErrorAs(t, e, &qe)
	require.Equal(t, kind, qe.Kind)
	require.Empty(t, p.Messages)
}

func checkClaudeParallelStrays(s *CommonStoriesSuite) {
	t := s.T()
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		queryLine("a1", "u", "assistant", "", `{"id":"R","content":[{"type":"tool_use","id":"t1"}]}`) +
		queryLine("a2", "a1", "assistant", "", `{"id":"R","content":[{"type":"tool_use","id":"t2"}]}`) +
		queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("stray", "u", "user", `"sourceToolAssistantUUID":"a1",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("already-answered", "u", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("another-writer", "u", "user", `"agentId":"child",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("empty-writer", "u", "user", `"agentId":"",`, `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("end", "r2", "assistant", "", `{"id":"end","content":"done"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "a1", "a2", "stray", "r2", "end"}, ids)
}

func checkClaudeQueryDefaultReturnBound(s *CommonStoriesSuite) {
	t := s.T()
	var source strings.Builder
	parent := ""
	for i := 0; i < 1001; i++ {
		id := fmt.Sprint(i)
		source.WriteString(queryLine(id, parent, "user", "", `{"content":"saved"}`))
		parent = id
	}
	scope, _ := claudeQueryFixture(t, source.String())
	p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1000)
	require.Equal(t, "999", p.Messages[999].UUID)
	p.Messages[0].Message[0] = 'x'
	fresh, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1000, Limit: 1})
	require.NoError(t, e)
	require.Len(t, fresh.Messages, 1)
	require.Equal(t, "1000", fresh.Messages[0].UUID)
}

func checkClaudeQueryAlongsideSession(s *CommonStoriesSuite) {
	t := s.T()
	o, trace := claudeTargetOptions(t, "SDK-US-009")
	session, e := claude.Open(deadline(t), o)
	require.NoError(t, e)
	t.Cleanup(func() { _ = session.Close() })
	claudeUntil(t, session, "init")
	_, e = session.Submit(deadline(t), claude.Input{Text: "synthetic"})
	require.NoError(t, e)
	pending := claudeRequests(t, session, 2)
	require.NoError(t, session.Interrupt(deadline(t), claude.InterruptOptions{}))
	claudeUntil(t, session, "fixture_interrupt")
	before, e := os.ReadFile(trace)
	require.NoError(t, e)
	scope, _ := claudeQueryFixture(t, queryLine("saved", "", "user", "", `{"content":"stored"}`))
	p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, p.Messages, 1)
	listed, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, listed.Sessions, 1)
	metadata, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, e)
	require.Equal(t, "stored", metadata.Info.Summary)
	after, e := os.ReadFile(trace)
	require.NoError(t, e)
	require.Equal(t, before, after)
	require.Zero(t, countAnswers(t, trace, "claude"))
	require.NoError(t, pending[0].Deny(deadline(t), claude.DenialOptions{Message: "fixture decision"}))
	require.NoError(t, pending[1].Approve(deadline(t), claude.ApprovalOptions{}))
	require.NoError(t, session.Interrupt(deadline(t), claude.InterruptOptions{}))
	live := claudeUntil(t, session, "fixture_interrupt")
	for _, r := range live {
		if r.Event != nil {
			require.Nil(t, r.Event.History)
			require.NotContains(t, string(r.Raw), `"uuid":"saved"`)
		}
	}
	require.Equal(t, 2, countAnswers(t, trace, "claude"))
}

func checkClaudeNullToolIdentity(s *CommonStoriesSuite) {
	t := s.T()
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		queryLine("a", "u", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":null}]}`) +
		queryLine("unrelated", "u", "user", "", `{"content":[{"type":"tool_result","tool_use_id":null}]}`) +
		queryLine("end", "a", "assistant", "", `{"content":"done"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "a", "end"}, ids)
}

func checkClaudeRecoveredTimestampOrder(s *CommonStoriesSuite) {
	t := s.T()
	line := func(id, parent, kind, time, content string) string {
		return strings.Replace(queryLine(id, parent, kind, "", content), "2026-01-01T00:00:00Z", time, 1)
	}
	source := queryLine("u", "", "user", "", `{"content":"root"}`) +
		line("c0", "u", "assistant", "2026-01-01T00:00:00Z", `{"id":"R","content":[{"type":"tool_use","id":"t0"}]}`) +
		line("c2", "c1", "assistant", "2026-01-01T00:00:02Z", `{"id":"R","content":[{"type":"tool_use","id":"t2"}]}`) +
		line("c1", "c0", "assistant", "2026-01-01T00:00:01Z", `{"id":"R","content":[{"type":"tool_use","id":"t1"}]}`) +
		line("r1", "c1", "user", "2026-01-01T00:00:04Z", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		line("r2", "c2", "user", "2026-01-01T00:00:03Z", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		line("r0", "c0", "user", "2026-01-01T00:00:05Z", `{"content":[{"type":"tool_result","tool_use_id":"t0"}]}`) +
		queryLine("end", "r0", "assistant", "", `{"content":"end"}`)
	scope, _ := claudeQueryFixture(t, source)
	p, e := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, e)
	ids := []string{}
	for _, m := range p.Messages {
		ids = append(ids, m.UUID)
	}
	require.Equal(t, []string{"u", "c0", "c1", "c2", "r2", "r1", "r0", "end"}, ids)
}

func checkClaudeSessionMetadata(s *CommonStoriesSuite) {
	t := s.T()
	t.Setenv("PATH", t.TempDir())
	source := queryLine("u", "", "user", `"future":9007199254740993,`, `{"content":"first saved prompt"}`) +
		queryLine("a", "u", "assistant", "", `{"content":"answer"}`) +
		`{"type":"custom-title","customTitle":"Chosen title","future":null}` + "\n"
	scope, path := claudeQueryFixture(t, source)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	page, err := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, "complete", page.Coverage.Status)
	require.Len(t, page.Sessions, 1)
	info := page.Sessions[0]
	require.Equal(t, querySession, info.SessionID)
	require.Equal(t, "Chosen title", info.Summary)
	require.Equal(t, "first saved prompt", *info.FirstPrompt)
	require.Equal(t, "unknown", info.Running)
	require.False(t, info.Sampled)
	require.Contains(t, string(info.Head), "9007199254740993")
	single, err := claude.GetSessionInfo(deadline(t), info.SessionID, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.NotNil(t, single.Info)
	require.Equal(t, info, *single.Info)
	messages, err := claude.GetSessionMessages(deadline(t), info.SessionID, claude.SessionMessagesOptions{Scope: scope})
	require.NoError(t, err)
	require.Len(t, messages.Messages, 2)
	page.Sessions[0].Head[0] = 'x'
	again, err := claude.GetSessionInfo(deadline(t), info.SessionID, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.Equal(t, byte('{'), again.Info.Head[0])
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0400), stat.Mode().Perm())
}

func checkClaudeMetadataScopeAndPages(s *CommonStoriesSuite) {
	t := s.T()
	scope, path := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"one"}`))
	const other = "22222222-2222-4222-8222-222222222222"
	p2 := filepath.Join(filepath.Dir(path), other+".jsonl")
	require.NoError(t, os.WriteFile(p2, []byte(strings.ReplaceAll(queryLine("u", "", "user", "", `{"content":"two"}`), querySession, other)), 0400))
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	require.NoError(t, os.Chtimes(path, older, older))
	require.NoError(t, os.Chtimes(p2, newer, newer))
	page, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, other, page.Sessions[0].SessionID)
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope, Offset: 1, Limit: 1})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, querySession, page.Sessions[0].SessionID)
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope, Offset: 99})
	require.NoError(t, e)
	require.Empty(t, page.Sessions)
	require.Equal(t, "complete", page.Coverage.Status)
	require.NoError(t, os.Chtimes(path, newer, newer))
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 2)
	require.Equal(t, querySession, page.Sessions[0].SessionID)
	// Project scope must be reused by list, info and messages.
	project := t.TempDir()
	canonical, e := filepath.EvalSymlinks(project)
	require.NoError(t, e)
	key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonical, "-")
	dir := filepath.Join(scope.ConfigDir, "projects", key)
	require.NoError(t, os.Mkdir(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, other+".jsonl"), []byte(""), 0400))
	scoped := scope
	scoped.ProjectDir = project
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scoped})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
	require.Equal(t, other, page.Sessions[0].SessionID)
	require.Empty(t, page.Sessions[0].Summary)
	_, e = claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scoped})
	requireMetadataKind(t, e, "missing")
	// Discovery retains duplicate identity, never picks a newest winner.
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope})
	requireMetadataKind(t, e, "ambiguous")
	require.Equal(t, "partial", page.Coverage.Status)
	require.Len(t, page.Sessions, 3)
	_, e = claude.GetSessionInfo(deadline(t), other, claude.SessionInfoOptions{Scope: scope})
	requireMetadataKind(t, e, "ambiguous")
	// Known nested subagent layout is outside scope. A main file marked sidechain
	// is excluded, but metadata-only or unknown native objects remain discoverable.
	require.NoError(t, os.Mkdir(filepath.Join(dir, other), 0700))
	side := strings.ReplaceAll(queryLine("side", "", "user", `"isSidechain":true,`, `{"content":"hidden"}`), querySession, "33333333-3333-4333-8333-333333333333")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "33333333-3333-4333-8333-333333333333.jsonl"), []byte(side), 0400))
	page, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scoped})
	require.NoError(t, e)
	require.Len(t, page.Sessions, 1)
}

func requireMetadataKind(t *testing.T, err error, kind string) {
	t.Helper()
	var q *claude.SessionQueryError
	require.ErrorAs(t, err, &q)
	require.Equal(t, kind, q.Kind)
}

func checkClaudeMetadataSamples(s *CommonStoriesSuite) {
	cases := []struct{ name, source, summary, prompt string }{
		{"native-precedence", queryLine("u", "", "user", "", `{"content":"prompt"}`) + `{"type":"custom-title","aiTitle":"AI"}` + "\n" + `{"type":"last-prompt","lastPrompt":"recent"}` + "\n" + `{"type":"summary","summary":"older"}` + "\n", "AI", "prompt"},
		{"last-prompt-precedence", queryLine("u", "", "user", "", `{"content":"prompt"}`) + `{"type":"last-prompt","lastPrompt":"recent"}` + "\n" + `{"type":"summary","summary":"older"}` + "\n", "recent", "prompt"},
		{"skip-hook", queryLine("hook", "", "user", "", `{"content":"<session-start-hook>generated</session-start-hook>"}`) + queryLine("u", "hook", "user", "", `{"content":[{"type":"text","text":"actual prompt"}]}`), "actual prompt", "actual prompt"},
		{"command-fallback", queryLine("u", "", "user", "", `{"content":"<command-name>/help</command-name>"}`), "/help", "/help"},
		{"unknown", `{"type":"future-native","extension":{"n":9007199254740993}}` + "\n", "", ""},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			t := s.T()
			scope, _ := claudeQueryFixture(t, tc.source)
			result, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
			require.NoError(t, e)
			require.NotNil(t, result.Info)
			require.Equal(t, tc.summary, result.Info.Summary)
			if tc.prompt != "" {
				require.NotNil(t, result.Info.FirstPrompt)
				require.Equal(t, tc.prompt, *result.Info.FirstPrompt)
			}
		})
	}
	s.Run("bounded-middle", func() {
		t := s.T()
		head := queryLine("u", "", "user", "", `{"content":"sampled prompt"}`)
		// A corrupt unread middle proves metadata does not force message parsing.
		source := head + strings.Repeat("unread not JSON", 1000) + "\n" + `{"type":"custom-title","customTitle":"tail title","future":9007199254740993}` + "\n"
		scope, _ := claudeQueryFixture(t, source)
		result, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: 256})
		require.NoError(t, e)
		require.NotNil(t, result.Info)
		require.True(t, result.Info.Sampled)
		require.Equal(t, "tail title", result.Info.Summary)
		require.Equal(t, int64(513), result.Coverage.Bytes) // two 256-byte windows plus the tail boundary probe
		require.Equal(t, "complete", result.Coverage.Status)
		require.Contains(t, string(result.Info.Tail), "9007199254740993")
		_, e = claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireMetadataKind(t, e, "corrupt")
	})
}

func checkClaudeMetadataFailures(s *CommonStoriesSuite) {
	cases := []struct {
		name    string
		options func(claude.SessionQueryScope) claude.ListSessionsOptions
		kind    string
	}{
		{"negative-offset", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Offset: -1}
		}, "invalid-input"},
		{"negative-limit", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Limit: -1}
		}, "invalid-input"},
		{"negative-sample", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, SampleBytes: -1}
		}, "invalid-input"},
		{"entries", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			q.MaxEntries = 1
			return claude.ListSessionsOptions{Scope: q}
		}, "resource-limit"},
		{"bytes", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, MaxBytes: 1}
		}, "resource-limit"},
		{"lines", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, MaxLines: 1}
		}, "resource-limit"},
		{"page", func(q claude.SessionQueryScope) claude.ListSessionsOptions {
			return claude.ListSessionsOptions{Scope: q, Limit: 1001}
		}, "resource-limit"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			t := s.T()
			q, _ := claudeQueryFixture(t, queryLine("u", "", "user", "", `{"content":"prompt"}`)+queryLine("a", "u", "assistant", "", `{"content":"answer"}`))
			p, e := claude.ListSessions(deadline(t), tc.options(q))
			requireMetadataKind(t, e, tc.kind)
			require.NotEqual(t, "complete", p.Coverage.Status)
		})
	}
	for _, tc := range []struct{ name, source, kind string }{
		{"invalid-json", "{bad}\n", "corrupt"}, {"incomplete", "{\"type\":\"future\"}", "corrupt"},
		{"duplicate-key", "{\"x\":1,\"x\":2}\n", "corrupt"}, {"identity", "{\"sessionId\":\"other\"}\n", "corrupt"},
		{"title-type", "{\"customTitle\":5}\n", "corrupt"},
	} {
		s.Run(tc.name, func() {
			t := s.T()
			q, _ := claudeQueryFixture(t, tc.source)
			p, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
			requireMetadataKind(t, e, tc.kind)
			require.Equal(t, "partial", p.Coverage.Status)
			require.Len(t, p.Sessions, 1)
			r, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
			requireMetadataKind(t, e, tc.kind)
			require.NotNil(t, r.Info)
		})
	}
	s.Run("access-cancel-isolation", func() {
		t := s.T()
		q, path := claudeQueryFixture(t, `{"future":null}`+"\n")
		require.NoError(t, os.Chmod(path, 0000))
		t.Cleanup(func() { _ = os.Chmod(path, 0400) })
		p, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "inaccessible")
		require.ErrorIs(t, e, os.ErrPermission)
		require.Equal(t, "partial", p.Coverage.Status)
		require.Len(t, p.Sessions, 1)
		require.NoError(t, os.Chmod(path, 0400))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p, e = claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: q})
		require.ErrorIs(t, e, context.Canceled)
		require.Equal(t, "unknown", p.Coverage.Status)
		_, e = claude.GetSessionInfo(ctx, querySession, claude.SessionInfoOptions{Scope: q})
		require.ErrorIs(t, e, context.Canceled)
		_, e = claude.ListSessions(nil, claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "invalid-input")
		_, e = claude.GetSessionInfo(deadline(t), "../escape", claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "invalid-input")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "secret"), path))
		_, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		_, e = claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Remove(filepath.Dir(path)))
		require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(path)))
		_, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
	})
	s.Run("empty-missing-partial", func() {
		t := s.T()
		q, path := claudeQueryFixture(t, "")
		r, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		require.NoError(t, e)
		require.NotNil(t, r.Info)
		require.Equal(t, "complete", r.Coverage.Status)
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "legacy.jsonl"), []byte("private must not read"), 0000))
		p, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		requireMetadataKind(t, e, "unsupported")
		require.Len(t, p.Sessions, 1)
		require.Equal(t, "partial", p.Coverage.Status)
		require.NoError(t, os.Remove(filepath.Join(filepath.Dir(path), "legacy.jsonl")))
		require.NoError(t, os.Remove(path))
		p, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		require.NoError(t, e)
		require.Empty(t, p.Sessions)
		require.Equal(t, "complete", p.Coverage.Status)
		_, e = claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "missing")
		_, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: claude.SessionQueryScope{ConfigDir: t.TempDir()}})
		requireMetadataKind(t, e, "missing")
	})
}

func checkClaudeMetadataDefaultPage(s *CommonStoriesSuite) {
	t := s.T()
	q, path := claudeQueryFixture(t, "")
	for i := 2; i <= 102; i++ {
		id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i)
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), id+".jsonl"), nil, 0400))
	}
	p, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
	require.NoError(t, e)
	require.Len(t, p.Sessions, 100)
	require.Equal(t, "complete", p.Coverage.Status)
	p, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q, Offset: 100})
	require.NoError(t, e)
	require.Len(t, p.Sessions, 2)
	p, e = claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q, MaxSessions: 1})
	requireMetadataKind(t, e, "resource-limit")
	require.Len(t, p.Sessions, 1)
	require.Equal(t, "partial", p.Coverage.Status)
}

func checkClaudeMetadataSampleEdges(s *CommonStoriesSuite) {
	s.Run("head-is-not-tail", func() {
		t := s.T()
		source := strings.Repeat("\n", 256) + strings.Repeat("middle", 1000) + "\n" + queryLine("u", "", "user", "", `{"content":"tail only"}`)
		q, _ := claudeQueryFixture(t, source)
		r, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 256})
		require.NoError(t, e)
		require.True(t, r.Info.Sampled)
		require.Nil(t, r.Info.FirstPrompt)
		require.Nil(t, r.Info.CreatedAt)
	})
	s.Run("exact-entries", func() {
		t := s.T()
		q, _ := claudeQueryFixture(t, "")
		q.MaxEntries = 2
		p, e := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: q})
		require.NoError(t, e)
		require.Len(t, p.Sessions, 1)
	})
	s.Run("invalid-visibility-flag", func() {
		t := s.T()
		q, _ := claudeQueryFixture(t, queryLine("u", "", "user", `"isSidechain":"true",`, `{"content":"uncertain"}`))
		_, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q})
		requireMetadataKind(t, e, "corrupt")
	})
}

func checkClaudeMetadataIncompleteTail(s *CommonStoriesSuite) {
	t := s.T()
	q, _ := claudeQueryFixture(t, strings.Repeat("x", 1000))
	_, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 16})
	requireMetadataKind(t, e, "corrupt")
}

func checkClaudeMetadataNestedSampleProvenance(s *CommonStoriesSuite) {
	for _, extra := range []string{"", `"isSidechain":true,`} {
		s.Run("nested-"+extra, func() {
			t := s.T()
			tail := queryLine("tail", "", "user", extra, `{"content":"tail evidence"}`)
			source := `{"type":"future-native","nested":` + strings.TrimSpace(tail) + `,"padding":"` + strings.Repeat("x", 2000) + `"}` + "\n" + tail
			q, _ := claudeQueryFixture(t, source)
			r, e := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: q, SampleBytes: 300})
			require.NoError(t, e)
			require.NotNil(t, r.Info)
			require.True(t, r.Info.Sampled)
			require.Nil(t, r.Info.FirstPrompt)
			require.Nil(t, r.Info.CreatedAt)
		})
	}
}

func codexIndependentHistory(t *testing.T) {
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_STORY_PEER=codex", "SDK_STORY=US-014", "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "history-story", Version: "synthetic"}}})
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })
	page, err := c.ListThreads(ctx, ct.ThreadListParams{})
	must(t, err)
	require.Len(t, page.Data, 1)
	id := page.Data[0].ID
	assert.Equal(t, "saved", id)
	read, err := c.ReadThread(ctx, ct.ThreadReadParams{ThreadID: id, IncludeTurns: ct.Some(false)})
	must(t, err)
	assert.Equal(t, "preview", read.Thread.Preview)
	assert.Empty(t, read.Thread.Turns)
	assert.JSONEq(t, `{"future":true}`, string(read.UnknownFields["readState"]))
	turns, err := c.ListThreadTurns(ctx, ct.ThreadTurnsListParams{ThreadID: id})
	must(t, err)
	require.Len(t, turns.Data, 1)
	assert.Equal(t, "turn", turns.Data[0].ID)
	assert.Empty(t, turns.Data[0].Items, "summary is not full content")
	tail, err := c.ListThreadTurns(ctx, ct.ThreadTurnsListParams{ThreadID: id, Cursor: turns.NextCursor})
	must(t, err)
	assert.Empty(t, tail.Data)
	assert.True(t, tail.NextCursor.IsNull())
	items, err := c.ListThreadItems(ctx, ct.ThreadItemsListParams{ThreadID: id, TurnID: ct.Some("turn")})
	must(t, err)
	require.Len(t, items.Data, 1)
	assert.JSONEq(t, `{"type":"futureItem","id":"item","n":9007199254740993}`, string(items.Data[0].Item.Unknown))
	var methods []string
	must(t, c.Call(ctx, "fixture/query-trace", nil, &methods))
	assert.Equal(t, []string{"initialize", "initialized", "thread/list", "thread/read", "thread/turns/list", "thread/turns/list", "thread/items/list", "fixture/query-trace"}, methods, "query must not start/resume/subscribe/submit/answer")
}

// Browse metadata first, then page the selected content after compaction and
// parallel recovery. Expected order follows the pinned native algorithm, rather
// than JSONL line offsets or the preview's pre-compaction prompt.
func claudeIndependentHistory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	source := queryLine("old", "", "user", "", `{"content":"pre-compaction preview"}`) +
		`{"type":"system","uuid":"boundary","parentUuid":null,"sessionId":"` + querySession + `","subtype":"compact_boundary","logicalParentUuid":"old"}` + "\n" +
		queryLine("summary", "boundary", "user", `"isCompactSummary":true,`, `{"content":"retained summary"}`) +
		queryLine("a1", "summary", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t1"}]}`) +
		queryLine("a2", "a1", "assistant", "", `{"id":"response","content":[{"type":"tool_use","id":"t2"}]}`) +
		queryLine("r1", "a1", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t1"}]}`) +
		queryLine("r2", "a2", "user", "", `{"content":[{"type":"tool_result","tool_use_id":"t2"}]}`) +
		queryLine("end", "r1", "assistant", `"future":null,`, `{"content":[{"type":"future","n":9007199254740993}]}`) +
		queryLine("side", "summary", "assistant", `"isSidechain":true,`, `{"content":"hidden"}`)
	scope, path := claudeQueryFixture(t, source)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	ctx := deadline(t)
	listed, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, "complete", listed.Coverage.Status)
	require.Len(t, listed.Sessions, 1)
	id := listed.Sessions[0].SessionID
	info, err := claude.GetSessionInfo(ctx, id, claude.SessionInfoOptions{Scope: scope})
	require.NoError(t, err)
	require.NotNil(t, info.Info)
	assert.Equal(t, "pre-compaction preview", info.Info.Summary)
	assert.Equal(t, "unknown", info.Info.Running)
	var ids []string
	for offset := 0; offset <= 6; offset += 2 {
		page, err := claude.GetSessionMessages(ctx, id, claude.SessionMessagesOptions{Scope: scope, Offset: offset, Limit: 2})
		require.NoError(t, err)
		require.Equal(t, "complete", page.Coverage.Status)
		assert.False(t, page.Coverage.Atomic)
		if offset == 6 {
			require.Empty(t, page.Messages)
		} else {
			require.Len(t, page.Messages, 2)
		}
		for _, message := range page.Messages {
			ids = append(ids, message.UUID)
			assert.Equal(t, id, message.SessionID)
			if message.UUID == "end" {
				assert.JSONEq(t, `{"content":[{"type":"future","n":9007199254740993}]}`, string(message.Message))
				assert.Contains(t, string(message.Raw), `"future":null`)
			}
		}
	}
	assert.Equal(t, []string{"summary", "a1", "a2", "r2", "r1", "end"}, ids)
	exhausted, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Offset: 1, Limit: 1})
	require.NoError(t, err)
	assert.Empty(t, exhausted.Sessions)
	_, err = claude.GetSessionInfo(ctx, "22222222-2222-4222-8222-222222222222", claude.SessionInfoOptions{Scope: scope})
	requireMetadataKind(t, err, "missing")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
}

func checkClaudeMetadataTailLineBoundary(s *CommonStoriesSuite) {
	t := s.T()
	head := `{"type":"user","message":{"content":"hello"}}` + "\n"
	tail := `{"type":"custom-title","customTitle":"tail title"}` + "\n"
	source := head + `{"type":"summary","summary":"` + strings.Repeat("x", 1000) + `"}` + "\n" + tail
	scope, _ := claudeQueryFixture(t, source)
	sample := int64(len(tail))
	info, err := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: sample})
	require.NoError(t, err)
	require.NotNil(t, info.Info)
	assert.Equal(t, "tail title", info.Info.Summary)
	require.Len(t, info.Info.Records, 2)
	assert.Equal(t, tail, string(info.Info.Tail))
	assert.Equal(t, "hello", *info.Info.FirstPrompt)
	assert.True(t, info.Info.Sampled)
	// Both windows plus one preceding byte establish that the tail starts a line.
	assert.Equal(t, 2*sample+1, info.Coverage.Bytes)
	listed, err := claude.ListSessions(deadline(t), claude.ListSessionsOptions{Scope: scope, SampleBytes: sample, MaxBytes: 2*sample + 1})
	require.NoError(t, err)
	require.Len(t, listed.Sessions, 1)
	assert.Equal(t, "tail title", listed.Sessions[0].Summary)
	limited, err := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope, SampleBytes: sample, MaxBytes: 2 * sample})
	requireMetadataKind(t, err, "resource-limit")
	assert.Equal(t, "partial", limited.Coverage.Status)
	assert.Zero(t, limited.Coverage.Bytes, "reject before exceeding the byte budget")
}

func checkClaudeNonMessageRequestMetadata(s *CommonStoriesSuite) {
	for _, kind := range []string{"api-request-shape", "api-request-blob", "api-request", "cost-state"} {
		s.Run(kind, func() {
			t := s.T()
			// Artificial non-message record: no uuid/parentUuid and no real request data.
			metadata := fmt.Sprintf(`{"type":%q,"synthetic":{"future":9007199254740993,"nullable":null}}`, kind)
			source := queryLine("u", "", "user", "", `{"content":"question"}`) + metadata + "\n" + queryLine("a", "u", "assistant", "", `{"content":"answer"}`)
			scope, _ := claudeQueryFixture(t, source)
			page, err := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, MaxBytes: int64(len(source)), MaxLines: 3, MaxRecords: 2})
			require.NoError(t, err)
			require.Len(t, page.Messages, 2)
			assert.Equal(t, "u", page.Messages[0].UUID)
			assert.Equal(t, "a", page.Messages[1].UUID)
			assert.Equal(t, "complete", page.Coverage.Status)
			assert.Equal(t, 3, page.Coverage.Lines)
			assert.Equal(t, 2, page.Coverage.Records, "non-message metadata is not a chain entry")
			next, err := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 1, Limit: 1})
			require.NoError(t, err)
			require.Len(t, next.Messages, 1)
			assert.Equal(t, "a", next.Messages[0].UUID)
			end, err := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope, Offset: 2, Limit: 1})
			require.NoError(t, err)
			assert.Empty(t, end.Messages)
			info, err := claude.GetSessionInfo(deadline(t), querySession, claude.SessionInfoOptions{Scope: scope})
			require.NoError(t, err)
			require.NotNil(t, info.Info)
			require.Len(t, info.Info.Records, 3)
			assert.JSONEq(t, metadata, string(info.Info.Records[1]))
		})
	}
}

func checkClaudeRequestMetadataGuards(s *CommonStoriesSuite) {
	for _, kind := range []string{"api-request-shape", "api-request-blob", "api-request", "cost-state"} {
		s.Run(kind, func() {
			for _, tc := range []struct {
				name, extra, want string
				configure         func(*claude.SessionMessagesOptions)
			}{
				{name: "foreign-camel-identity", extra: `,"sessionId":"22222222-2222-4222-8222-222222222222"`, want: "corrupt"},
				{name: "foreign-snake-identity", extra: `,"session_id":"22222222-2222-4222-8222-222222222222"`, want: "corrupt"},
				{name: "typed-identity", extra: `,"sessionId":12`, want: "corrupt"},
				{name: "duplicate-identity", extra: `,"sessionId":null,"sessionId":null`, want: "corrupt"},
				{name: "invalid-unicode", extra: `,"synthetic":"\ud800"`, want: "corrupt"},
				{name: "lines", want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxLines = 2 }},
				{name: "bytes", want: "resource-limit"},
				{name: "index", want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxRecords = 1 }},
				{name: "large-metadata-line", extra: `,"synthetic":"` + strings.Repeat("x", 2048) + `"`, want: "resource-limit", configure: func(o *claude.SessionMessagesOptions) { o.MaxLineBytes = 1024 }},
			} {
				s.Run(tc.name, func() {
					t := s.T()
					metadata := fmt.Sprintf(`{"type":%q%s}`+"\n", kind, tc.extra)
					source := queryLine("u", "", "user", "", `{"content":"question"}`) + metadata + queryLine("a", "u", "assistant", "", `{"content":"answer"}`)
					scope, _ := claudeQueryFixture(t, source)
					options := claude.SessionMessagesOptions{Scope: scope}
					if tc.configure != nil {
						tc.configure(&options)
					}
					if tc.name == "bytes" {
						options.MaxBytes = int64(len(source) - 1)
					}
					page, err := claude.GetSessionMessages(deadline(t), querySession, options)
					requireQueryKind(t, page, err, tc.want)
				})
			}
		})
	}
	s.Run("unknown-remains-unsupported", func() {
		t := s.T()
		scope, _ := claudeQueryFixture(t, `{"type":"api-request-future"}`+"\n")
		page, err := claude.GetSessionMessages(deadline(t), querySession, claude.SessionMessagesOptions{Scope: scope})
		requireQueryKind(t, page, err, "unsupported")
	})
}
