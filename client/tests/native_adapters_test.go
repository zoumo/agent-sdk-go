package client_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/zoumo/agent-sdk-go/client/claude"
	clt "github.com/zoumo/agent-sdk-go/client/claude/types"
	"github.com/zoumo/agent-sdk-go/client/codex"
	ct "github.com/zoumo/agent-sdk-go/client/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
	codextest "github.com/zoumo/agent-sdk-go/internal/testsupport/codex"
)

// NativeAdaptersSuite runs the same acceptance table through isolated native
// lifecycle adapters. The adapters keep each original answer handle local.
type nativeAdapter struct {
	name string
	run  func(*testing.T, string) nativeTrace
}
type NativeAdaptersSuite struct {
	suite.Suite
	adapters []nativeAdapter
}

func (s *NativeAdaptersSuite) SetupSuite() {
	s.adapters = []nativeAdapter{{"codex", codexScenario}, {"claude", claudeScenario}}
}

func TestNativeAdapterMatrix(t *testing.T) { suite.Run(t, new(NativeAdaptersSuite)) }

func (s *NativeAdaptersSuite) TestNativeAdapters() {
	for _, scenario := range nativeScenarios {
		s.Run(scenario, func() {
			modes := []string{""}
			if scenario == "permission" {
				modes = append(modes, "-allow")
			}
			if scenario == "canceled-answer" {
				modes = append(modes, "-close")
			}
			for _, adapter := range s.adapters {
				for _, suffix := range modes {
					s.Run(adapter.name+suffix, func() {
						t := s.T()
						s.checkScenario(scenario+suffix, adapter.run(t, scenario+suffix))
					})
				}
			}
		})
	}
}

// nativeObservation is a detached test fact extracted by a native adapter. Raw keeps
// the observed wire payload; it is never used to construct response authority.
type (
	nativeObservation struct{ Kind, ID, Parent, Raw string }
	nativeTrace       struct {
		Events                                                                                       []nativeObservation
		SendError, AnswerError, RetryError, CanceledError, OverflowError, InterruptError, CloseError error
		AnswerID, AnswerText, AnswerDecision                                                         string
		AnswerCount, InputCount                                                                      int
	}
)

var nativeScenarios = []string{"submission-barrier", "tool-boundary", "child-authority", "nested-parentage", "child-after-result", "permission", "question", "canceled-answer", "backpressure", "unknown-fields", "history-observation", "interrupt-boundary"}

// checkScenario applies common user-visible acceptance rules, without requiring native
// events to share a schema, an ordering beyond causal boundaries, or dedup rules.
func (s *NativeAdaptersSuite) checkScenario(scenario string, got nativeTrace) {
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
	switch scenario {
	case "submission-barrier":
		check.NoError(got.SendError, "input submission failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "input barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "send acknowledgement confused with task completion")
	case "tool-boundary":
		check.GreaterOrEqual(find("tool", ""), 0, "tool completion missing")
		check.Greater(find("complete", "root"), find("tool", ""), "tool completion require.NoError remain separate from task completion")
	case "child-authority":
		found := false
		for _, e := range got.Events {
			if e.Kind == "associated" && e.ID == "child" && e.Parent == "root" {
				found = true
			}
		}
		check.True(found, "confirmed child parent lost")
		check.GreaterOrEqual(find("native", "child"), 0, "child native source lost")
	case "nested-parentage":
		for _, pair := range [][2]string{{"child", "root"}, {"sibling", "root"}, {"grand", "child"}} {
			found := false
			for _, e := range got.Events {
				if e.Kind == "associated" && e.ID == pair[0] && e.Parent == pair[1] {
					found = true
				}
			}
			check.True(found, "nested/parallel relation lost: "+pair[0])
		}
		check.GreaterOrEqual(find("gap", ""), 0, "unknown source require.NoError produce visible uncertainty")
		check.Negative(find("associated", "unknown"), "unknown parent guessed")
	case "child-after-result":
		check.GreaterOrEqual(find("complete", "root"), 0, "root completion missing")
		check.Greater(find("native", "child"), find("complete", "root"), "child observation ended at root completion")
	case "permission", "permission-allow", "question":
		check.NoError(got.AnswerError, "original handle first answer")
		check.Error(got.RetryError, "original handle require.NoError be single use")
		check.Equal(1, got.AnswerCount, "answer uniqueness lost")
		check.Equal("original", got.AnswerID, "answer correlation lost")
		if scenario == "permission" {
			check.Equal("deny", got.AnswerDecision, "native permission refusal changed")
		}
		if scenario == "permission-allow" {
			check.Equal("allow", got.AnswerDecision, "native permission approval changed")
		}
		if scenario == "question" {
			check.Equal("free text / 自由文本", got.AnswerText, "answer not mapped to original question")
		}
	case "canceled-answer":
		check.ErrorIs(got.CanceledError, context.Canceled, "canceled answer require.NoError retain native context cancellation")
		check.NoError(got.AnswerError, "pre-canceled answer require.NoError leave original handle available under native contract")
		check.Equal(1, got.AnswerCount, "pre-canceled answer require.NoError not consume handle")
	case "canceled-answer-close":
		check.NoError(got.CloseError, "close failed")
		check.Error(got.AnswerError, "closing while waiting for an answer require.NoError invalidate the original handle")
		check.Error(got.RetryError, "closed original handle require.NoError stay invalid")
	case "backpressure":
		check.NoError(got.OverflowError, "slow consumer terminated observation")
		bursts := 0
		for _, event := range got.Events {
			if event.Kind == "burst" {
				bursts++
			}
		}
		check.Equal(32, bursts, "accepted burst require.NoError drain completely")
		check.Equal("barrier", got.Events[len(got.Events)-1].Kind, "burst barrier require.NoError remain last")
	case "unknown-fields":
		unknown := 0
		for _, e := range got.Events {
			if e.Kind == "unknown" {
				unknown++
				check.Contains(e.Raw, `"future":{"big":9007199254740993,"nullable":null}`, "unknown payload changed")
			}
		}
		check.Equal(2, unknown, "unknown no-ID arrivals require.NoError survive independently")
	case "history-observation":
		check.GreaterOrEqual(find("gap", ""), 0, "history recovery gap lost")
		check.GreaterOrEqual(find("associated", "child"), 0, "history association missing")
		check.Negative(find("created", "child"), "history association invented creation")
		check.Zero(got.InputCount, "recovery replayed input")
		check.Zero(got.AnswerCount, "recovery replayed answer")
	case "interrupt-boundary":
		check.NoError(got.InterruptError, "interrupt failed")
		check.NoError(got.CloseError, "close failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "interrupt barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "interrupt acknowledgement fabricated completion")
	}
}

func codexScenario(t *testing.T, scenario string) (trace nativeTrace) {
	mode := scenario
	scenario = strings.TrimSuffix(strings.TrimSuffix(scenario, "-allow"), "-close")
	ctx := testsupport.Deadline(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_INTEGRATION_PEER=codex", "SDK_INTEGRATION=" + scenario, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "integration", Version: "synthetic"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	s, err := codex.Open(ctx, codex.SessionOptions{Client: c})
	require.NoError(t, err)
	defer s.Abort()
	readUntil := func(target string) {
		for {
			r, e := s.Receive(ctx)
			require.NoError(t, e)
			if scenario == "child-authority" {
				require.Nil(t, r.Request, "relationship minted answer authority")
			}
			if r.Event != nil {
				codexFact(&trace, *r.Event)
				if r.Event.Method == "fixture/"+target {
					return
				}
			}
			if r.Observation != nil {
				v := r.Observation
				if v.Reason == "parent-confirmed" {
					trace.Events = append(trace.Events, nativeObservation{Kind: "associated", ID: v.ThreadID, Parent: v.ParentThreadID})
				}
			}
			if s.Unattributed().CoverageGap {
				trace.Events = append(trace.Events, nativeObservation{Kind: "gap"})
			}
		}
	}

	if scenario == "history-observation" {
		page, e := s.ListSubagents(ctx, codex.SubagentQuery{})
		require.NoError(t, e)
		require.Len(t, page.Pages, 1)
		for _, r := range page.Pages[0].Data {
			trace.Events = append(trace.Events, nativeObservation{Kind: "associated", ID: r.ID, Parent: "root"})
		}
		require.False(t, page.Coverage.Archived)
		trace.Events = append(trace.Events, nativeObservation{Kind: "gap", Raw: "non-archived records only; live observation unconfirmed"})
		return trace
	}
	_, trace.SendError = s.Submit(ctx, codex.Input{})
	if scenario == "backpressure" {
		require.NoError(t, trace.SendError) // reply follows the complete burst while Events is unread
		readUntil("barrier")
		trace.OverflowError = s.Err()
		return trace
	}
	if scenario == "permission" || scenario == "question" || scenario == "canceled-answer" {
		r := codextest.Requests(t, s, 1)[0]
		require.NotEmpty(t, r.ID().String(), "missing native request identity")
		_, err := r.Data()
		require.NoError(t, err)
		if strings.HasSuffix(mode, "-close") {
			trace.CloseError = s.Close()
			trace.AnswerError = r.Handle().Answer(ctx, nil)
			trace.RetryError = r.Handle().Answer(ctx, nil)
			return trace
		}
		answer := json.RawMessage(`{"decision":"decline"}`)
		if strings.HasSuffix(mode, "-allow") {
			answer = json.RawMessage(`{"decision":"accept"}`)
		}
		if scenario != "permission" {
			var questions ct.ToolRequestUserInputParams
			require.NoError(t, json.Unmarshal(r.Params(), &questions))
			require.Len(t, questions.Questions, 1, "question identity lost")
			assert.Equal(t, "q", questions.Questions[0].ID, "question identity lost")
			answer = json.RawMessage(`{"answers":{"q":{"answers":["free text / 自由文本"]}}}`)
		}
		handle := r.Handle()
		if scenario == "canceled-answer" {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			trace.CanceledError = handle.Answer(canceled, answer)
		}
		trace.AnswerError = handle.Answer(ctx, answer)
		trace.RetryError = handle.Answer(ctx, answer)
		readUntil("snapshot")
		return trace
	}
	readUntil("barrier")
	if scenario == "child-authority" {
		var forged codex.AnswerHandle
		assert.Error(t, forged.Answer(ctx, nil), "zero handle answered")
	}
	if scenario == "interrupt-boundary" {
		trace.InterruptError = s.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"})
	}
	if scenario == "submission-barrier" || scenario == "tool-boundary" || scenario == "interrupt-boundary" {
		require.NoError(t, c.Call(ctx, "fixture/release", nil, nil))
		readUntil("end")
	}
	if scenario == "interrupt-boundary" {
		trace.CloseError = s.Close()
		require.NoError(t, c.Call(ctx, "fixture/snapshot", nil, nil))
	}
	return trace
}

func codexFact(trace *nativeTrace, e ct.Event) {
	add := func(kind, id, parent string) {
		trace.Events = append(trace.Events, nativeObservation{Kind: kind, ID: id, Parent: parent, Raw: string(e.Raw)})
	}
	var p struct {
		ThreadID        string `json:"threadId"`
		Inputs, Answers int
		Wire            json.RawMessage
	}
	_ = json.Unmarshal(e.Params, &p)
	switch e.Method {
	case "turn/completed":
		add("complete", p.ThreadID, "")
	case "item/completed":
		add("tool", p.ThreadID, "")
	case "item/agentMessage/delta":
		add("native", p.ThreadID, "")
	case "fixture/burst":
		add("burst", "", "")
	case "fixture/barrier":
		add("barrier", "", "")
	case "fixture/unknown":
		add("unknown", "", "")
	case "fixture/snapshot":
		trace.InputCount = p.Inputs
		trace.AnswerCount = p.Answers
		var wire struct {
			ID     string
			Result struct {
				Decision string
				Answers  map[string]struct{ Answers []string }
			}
		}
		_ = json.Unmarshal(p.Wire, &wire)
		trace.AnswerID = wire.ID
		switch wire.Result.Decision {
		case "accept":
			trace.AnswerDecision = "allow"
		case "decline":
			trace.AnswerDecision = "deny"
		}
		if a := wire.Result.Answers["q"].Answers; len(a) > 0 {
			trace.AnswerText = a[0]
		}
	}
}

func claudeScenario(t *testing.T, scenario string) (trace nativeTrace) {
	mode := scenario
	scenario = strings.TrimSuffix(strings.TrimSuffix(scenario, "-allow"), "-close")
	ctx := testsupport.Deadline(t)
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()

	opts := claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "SDK_INTEGRATION_PEER=claude", "SDK_INTEGRATION=" + scenario, "GORACE=atexit_sleep_ms=0"}, Subagents: claude.SubagentOptions{MaxUnresolvedBytes: 1}}
	if scenario == "history-observation" {
		opts.Subagents.History = []claude.SubagentHistory{{SessionID: "s", TaskID: "child"}}
	}
	c, err := claude.Open(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	readUntil := func(target string) {
		for {
			r, err := c.Receive(ctx)
			require.NoError(t, err)
			if scenario == "child-authority" {
				require.Nil(t, r.Request, "relationship minted answer authority")
			}
			if r.Event != nil {
				n := *r.Event
				claudeFact(&trace, n)
				if claudetest.FixtureKind(n.Raw) == "fixture_"+target {
					return
				}
			}
			if r.Observation != nil {
				v := r.Observation
				id := v.Source.TaskID
				parent := ""
				if v.Parent != nil {
					parent = v.Parent.TaskID
					if parent == "" {
						parent = "root"
					}
				}
				if v.Reason == "parent-confirmed" {
					trace.Events = append(trace.Events, nativeObservation{Kind: "associated", ID: id, Parent: parent})
				}
				if v.Reason == "member-confirmed" && scenario == "history-observation" {
					trace.Events = append(trace.Events, nativeObservation{Kind: "associated", ID: id})
				}
				if strings.Contains(v.Reason, "unconfirmed") || strings.Contains(v.Reason, "unverified") || v.Reason == "forwarding-not-confirmed" {
					trace.Events = append(trace.Events, nativeObservation{Kind: "gap"})
				}
			}
			if c.Unattributed().CoverageGap {
				trace.Events = append(trace.Events, nativeObservation{Kind: "gap"})
			}
		}
	}
	if scenario == "history-observation" {
		readUntil("snapshot")
		return trace
	}
	_, trace.SendError = c.Submit(ctx, claude.Input{Text: "start"})
	if scenario == "backpressure" {
		require.NoError(t, c.Interrupt(ctx, claude.InterruptOptions{})) // control reply follows the complete unread burst
		readUntil("barrier")
		trace.OverflowError = c.Err()
		return trace
	}
	if scenario == "permission" || scenario == "question" || scenario == "canceled-answer" {
		incoming := claudetest.Requests(t, c, 1)[0]
		r := incoming.Handle()
		require.NotNil(t, r)
		assert.Equal(t, "original", r.RequestID(), "request ID changed")
		if strings.HasSuffix(mode, "-close") {
			trace.CloseError = c.Close()
			trace.AnswerError = r.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny})
			trace.RetryError = r.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny})
			return trace
		}
		metadata, err := r.Metadata()
		require.NoError(t, err)
		metadata.ToolUseID = "tampered"
		answer := claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "declined"}
		if strings.HasSuffix(mode, "-allow") {
			answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow}
		}
		if scenario != "permission" {
			var questions clt.AskUserQuestionInput
			require.NoError(t, json.Unmarshal(r.Input(), &questions))
			require.Len(t, questions.Questions, 1, "question lost")
			text := "free text / 自由文本"
			updated, err := questions.WithAnswers(map[string]clt.QuestionAnswer{questions.Questions[0].Question: {Text: &text}})
			require.NoError(t, err)
			answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}
		}
		if scenario == "canceled-answer" {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			trace.CanceledError = r.Answer(canceled, answer)
		}
		trace.AnswerError = r.Answer(ctx, answer)
		trace.RetryError = r.Answer(ctx, answer)
		readUntil("snapshot")
		return trace
	}
	readUntil("barrier")
	if scenario == "child-authority" {
		var forged claude.PermissionRequest
		assert.Error(t, forged.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny}), "zero permission handle answered")
	}
	if scenario == "interrupt-boundary" {
		trace.InterruptError = c.Interrupt(ctx, claude.InterruptOptions{})
	}
	if scenario == "submission-barrier" || scenario == "tool-boundary" || scenario == "interrupt-boundary" {
		_, err = c.Submit(ctx, claude.Input{Text: "release"})
		require.NoError(t, err)
		readUntil("end")
	}
	if scenario == "interrupt-boundary" {
		trace.CloseError = c.Close()
	}
	return trace
}

func claudeFact(trace *nativeTrace, v claude.Event) {
	add := func(kind, id, parent, raw string) {
		trace.Events = append(trace.Events, nativeObservation{Kind: kind, ID: id, Parent: parent, Raw: raw})
	}
	kind := v.Type
	if claudetest.FixtureKind(v.Raw) != "" {
		kind = claudetest.FixtureKind(v.Raw)
	}
	switch kind {
	case "result":
		add("complete", "root", "", string(v.Raw))
	case "user":
		if v.User != nil {
			add("tool", "root", "", string(v.Raw))
		}
	case "assistant":
		if v.AgentID != nil {
			add("native", *v.AgentID, "", string(v.Raw))
		}
	case "fixture_burst":
		add("burst", "", "", string(v.Raw))
	case "fixture_barrier":
		add("barrier", "", "", string(v.Raw))
	case "fixture_unknown":
		add("unknown", "", "", string(v.Raw))
	case "fixture_snapshot":
		var p struct {
			Inputs, Answers int
			Wire            clt.ControlResponse
		}
		_ = json.Unmarshal(v.Raw, &p)
		trace.InputCount = p.Inputs
		trace.AnswerCount = p.Answers
		trace.AnswerID = p.Wire.Response.RequestID
		var result clt.PermissionResult
		_ = json.Unmarshal(p.Wire.Response.Response, &result)
		trace.AnswerDecision = string(result.Behavior)
		var input clt.AskUserQuestionInput
		_ = json.Unmarshal(result.UpdatedInput, &input)
		if a := input.Answers["Choose?"].Text; a != nil {
			trace.AnswerText = *a
		}
	}
}

// Retain the focused Codex history-query entrypoint. The combined native browser and
// Claude local-query acceptance live in history_query_test.go.
