package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	claude "github.com/zoumo/agent-sdk-go/claude"
	clt "github.com/zoumo/agent-sdk-go/claude/types"
	codex "github.com/zoumo/agent-sdk-go/codex"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
)

// NativeIntegrationSuite runs the same acceptance table through isolated native
// lifecycle adapters. The adapters keep each original answer handle local.
type nativeAdapter struct {
	name string
	run  func(*testing.T, string) nativeTrace
}
type NativeIntegrationSuite struct {
	suite.Suite
	adapters []nativeAdapter
}

func (s *NativeIntegrationSuite) SetupSuite() {
	s.adapters = []nativeAdapter{{"codex", codexScenario}, {"claude", claudeScenario}}
}

func TestNativeIntegration(t *testing.T) { suite.Run(t, new(NativeIntegrationSuite)) }
func (s *NativeIntegrationSuite) TestSessionTargets() {
	for _, id := range []string{"session-persistent-inputs", "session-native-input-fields", "session-submit-rejection", "session-tool-completion", "session-unknown-events", "session-child-association", "session-child-depth", "session-subagent-query", "session-approval", "session-canceled-approval", "session-question", "session-model-change", "session-thinking-change", "session-interrupt", "session-slow-consumer", "session-resume", "session-error-result", "session-close"} {
		s.Run(id, func() {
			s.Run("codex", func() { codexSessionTarget(s.T(), id) })
			s.Run("claude", func() { claudeSessionTarget(s.T(), id) })
		})
	}
}
func (s *NativeIntegrationSuite) TestNativeAdapters() {
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
func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func must(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}
func wait(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
}

// Adapters deliberately retain native clients, native types and original answer
// handles locally. Only detached facts reach the suite acceptance assertions.
func codexScenario(t *testing.T, scenario string) (trace nativeTrace) {
	mode := scenario
	scenario = strings.TrimSuffix(strings.TrimSuffix(scenario, "-allow"), "-close")
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"AGENT_MOCK_BIN=" + os.Getenv("AGENT_MOCK_BIN"), "SDK_INTEGRATION_PEER=codex", "SDK_INTEGRATION=" + scenario, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "integration", Version: "synthetic"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}})
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })

	s, err := codex.Open(ctx, codex.SessionOptions{Client: c})
	must(t, err)
	defer s.Abort()
	readUntil := func(target string) {
		for {
			r, e := s.Receive(ctx)
			must(t, e)
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
		must(t, e)
		require.Len(t, page.Pages, 1)
		for _, r := range page.Pages[0].Data {
			trace.Events = append(trace.Events, nativeObservation{Kind: "associated", ID: r.ID, Parent: "root"})
		}
		require.False(t, page.Coverage.Archived)
		trace.Events = append(trace.Events, nativeObservation{Kind: "gap", Raw: "non-archived records only; live observation unconfirmed"})
		return
	}
	_, trace.SendError = s.Submit(ctx, codex.Input{})
	if scenario == "backpressure" {
		must(t, trace.SendError) // reply follows the complete burst while Events is unread
		readUntil("barrier")
		trace.OverflowError = s.Err()
		return
	}
	if scenario == "permission" || scenario == "question" || scenario == "canceled-answer" {
		r := codexRequests(t, s, 1)[0]
		require.NotEmpty(t, r.ID().String(), "missing native request identity")
		_, err := r.Data()
		must(t, err)
		if strings.HasSuffix(mode, "-close") {
			trace.CloseError = s.Close()
			trace.AnswerError = r.Handle().Answer(ctx, nil)
			trace.RetryError = r.Handle().Answer(ctx, nil)
			return
		}
		answer := json.RawMessage(`{"decision":"decline"}`)
		if strings.HasSuffix(mode, "-allow") {
			answer = json.RawMessage(`{"decision":"accept"}`)
		}
		if scenario != "permission" {
			var questions ct.ToolRequestUserInputParams
			must(t, json.Unmarshal(r.Params(), &questions))
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
		return
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
		must(t, c.Call(ctx, "fixture/release", nil, nil))
		readUntil("end")
	}
	if scenario == "interrupt-boundary" {
		trace.CloseError = s.Close()
		must(t, c.Call(ctx, "fixture/snapshot", nil, nil))
	}
	return
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
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	dir := t.TempDir()

	opts := claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"AGENT_MOCK_BIN=" + os.Getenv("AGENT_MOCK_BIN"), "HOME=" + dir, "SDK_INTEGRATION_PEER=claude", "SDK_INTEGRATION=" + scenario, "GORACE=atexit_sleep_ms=0"}, Subagents: claude.SubagentOptions{MaxUnresolvedBytes: 1}}
	if scenario == "history-observation" {
		opts.Subagents.History = []claude.SubagentHistory{{SessionID: "s", TaskID: "child"}}
	}
	c, err := claude.Open(ctx, opts)
	must(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	readUntil := func(target string) {
		for {
			r, err := c.Receive(ctx)
			must(t, err)
			if scenario == "child-authority" {
				require.Nil(t, r.Request, "relationship minted answer authority")
			}
			if r.Event != nil {
				n := *r.Event
				claudeFact(&trace, n)
				if fixtureKind(n.Raw) == "fixture_"+target {
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
		return
	}
	_, trace.SendError = c.Submit(ctx, claude.Input{Text: "start"})
	if scenario == "backpressure" {
		must(t, c.Interrupt(ctx, claude.InterruptOptions{})) // control reply follows the complete unread burst
		readUntil("barrier")
		trace.OverflowError = c.Err()
		return
	}
	if scenario == "permission" || scenario == "question" || scenario == "canceled-answer" {
		incoming := claudeRequests(t, c, 1)[0]
		r := incoming.Handle()
		require.NotNil(t, r)
		assert.Equal(t, "original", r.RequestID(), "request ID changed")
		if strings.HasSuffix(mode, "-close") {
			trace.CloseError = c.Close()
			trace.AnswerError = r.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny})
			trace.RetryError = r.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny})
			return
		}
		metadata, err := r.Metadata()
		must(t, err)
		metadata.ToolUseID = "tampered"
		answer := claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "declined"}
		if strings.HasSuffix(mode, "-allow") {
			answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow}
		}
		if scenario != "permission" {
			var questions clt.AskUserQuestionInput
			must(t, json.Unmarshal(r.Input(), &questions))
			require.Len(t, questions.Questions, 1, "question lost")
			text := "free text / 自由文本"
			updated, err := questions.WithAnswers(map[string]clt.QuestionAnswer{questions.Questions[0].Question: {Text: &text}})
			must(t, err)
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
		return
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
		must(t, err)
		readUntil("end")
	}
	if scenario == "interrupt-boundary" {
		trace.CloseError = c.Close()
	}
	return
}
func claudeFact(trace *nativeTrace, v claude.Event) {
	add := func(kind, id, parent, raw string) {
		trace.Events = append(trace.Events, nativeObservation{Kind: kind, ID: id, Parent: parent, Raw: raw})
	}
	kind := v.Type
	if fixtureKind(v.Raw) != "" {
		kind = fixtureKind(v.Raw)
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
func (s *NativeIntegrationSuite) TestIndependentHistoryCodex() {
	s.Run("history-query/codex", func() { codexIndependentHistory(s.T()) })
}
