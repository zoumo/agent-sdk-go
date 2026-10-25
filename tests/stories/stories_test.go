package stories_test

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

// CommonStoriesSuite runs the same acceptance table through isolated native
// lifecycle adapters. The adapters keep each original answer handle local.
type storyAdapter struct {
	name string
	run  func(*testing.T, string) storyTrace
}
type CommonStoriesSuite struct {
	suite.Suite
	adapters []storyAdapter
}

func (s *CommonStoriesSuite) SetupSuite() {
	s.adapters = []storyAdapter{{"codex", codexStory}, {"claude", claudeStory}}
}

func TestCommonStories(t *testing.T) { suite.Run(t, new(CommonStoriesSuite)) }
func (s *CommonStoriesSuite) TestSessionTargets() {
	for _, id := range []string{"SDK-US-001", "SDK-US-002", "SDK-US-003", "SDK-US-004", "SDK-US-005", "SDK-US-006", "SDK-US-007", "SDK-US-008", "SDK-US-009", "SDK-US-010", "SDK-US-011", "SDK-US-012", "SDK-US-013", "SDK-US-014", "SDK-US-015", "SDK-US-016", "SDK-US-017", "SDK-US-018"} {
		s.Run(id, func() {
			s.Run("codex", func() { codexSessionTarget(s.T(), id) })
			s.Run("claude", func() { claudeSessionTarget(s.T(), id) })
		})
	}
}
func (s *CommonStoriesSuite) TestNativeAdapters() {
	for _, story := range storyIDs {
		s.Run(story, func() {
			modes := []string{""}
			if story == "US-006" {
				modes = append(modes, "-allow")
			}
			if story == "US-008" {
				modes = append(modes, "-close")
			}
			for _, adapter := range s.adapters {
				for _, suffix := range modes {
					s.Run(adapter.name+suffix, func() {
						t := s.T()
						s.checkStory(story+suffix, adapter.run(t, story+suffix))
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
func codexStory(t *testing.T, story string) (trace storyTrace) {
	mode := story
	story = strings.TrimSuffix(strings.TrimSuffix(story, "-allow"), "-close")
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_STORY_PEER=codex", "SDK_STORY=" + story, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "stories", Version: "synthetic"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}})
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })

	s, err := codex.Open(ctx, codex.SessionOptions{Client: c})
	must(t, err)
	defer s.Abort()
	readUntil := func(target string) {
		for {
			r, e := s.Receive(ctx)
			must(t, e)
			if story == "US-003" {
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
					trace.Events = append(trace.Events, storyObservation{Kind: "associated", ID: v.ThreadID, Parent: v.ParentThreadID})
				}
			}
			if s.Unattributed().CoverageGap {
				trace.Events = append(trace.Events, storyObservation{Kind: "gap"})
			}
		}
	}

	if story == "US-011" {
		page, e := s.ListSubagents(ctx, codex.SubagentQuery{})
		must(t, e)
		require.Len(t, page.Pages, 1)
		for _, r := range page.Pages[0].Data {
			trace.Events = append(trace.Events, storyObservation{Kind: "associated", ID: r.ID, Parent: "root"})
		}
		require.False(t, page.Coverage.Archived)
		trace.Events = append(trace.Events, storyObservation{Kind: "gap", Raw: "non-archived records only; live observation unconfirmed"})
		return
	}
	_, trace.SendError = s.Submit(ctx, codex.Input{})
	if story == "US-009" {
		must(t, trace.SendError) // reply follows the complete burst while Events is unread
		readUntil("barrier")
		trace.OverflowError = s.Err()
		return
	}
	if story == "US-006" || story == "US-007" || story == "US-008" {
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
		if story != "US-006" {
			var questions ct.ToolRequestUserInputParams
			must(t, json.Unmarshal(r.Params(), &questions))
			require.Len(t, questions.Questions, 1, "question identity lost")
			assert.Equal(t, "q", questions.Questions[0].ID, "question identity lost")
			answer = json.RawMessage(`{"answers":{"q":{"answers":["free text / 自由文本"]}}}`)
		}
		handle := r.Handle()
		if story == "US-008" {
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
	if story == "US-003" {

		var forged codex.AnswerHandle
		assert.Error(t, forged.Answer(ctx, nil), "zero handle answered")
	}
	if story == "US-012" {
		trace.InterruptError = s.Interrupt(ctx, codex.InterruptOptions{TurnID: "turn"})
	}
	if story == "US-001" || story == "US-002" || story == "US-012" {
		must(t, c.Call(ctx, "fixture/release", nil, nil))
		readUntil("end")
	}
	if story == "US-012" {
		trace.CloseError = s.Close()
		must(t, c.Call(ctx, "fixture/snapshot", nil, nil))
	}
	return
}
func codexFact(trace *storyTrace, e ct.Event) {
	add := func(kind, id, parent string) {
		trace.Events = append(trace.Events, storyObservation{Kind: kind, ID: id, Parent: parent, Raw: string(e.Raw)})
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

func claudeStory(t *testing.T, story string) (trace storyTrace) {
	mode := story
	story = strings.TrimSuffix(strings.TrimSuffix(story, "-allow"), "-close")
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	dir := t.TempDir()

	opts := claude.SessionOptions{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "SDK_STORY_PEER=claude", "SDK_STORY=" + story, "GORACE=atexit_sleep_ms=0"}, Subagents: claude.SubagentOptions{MaxUnresolvedBytes: 1}}
	if story == "US-011" {
		opts.Subagents.History = []claude.SubagentHistory{{SessionID: "s", TaskID: "child"}}
	}
	c, err := claude.Open(ctx, opts)
	must(t, err)
	t.Cleanup(func() { _ = c.Abort() })
	readUntil := func(target string) {
		for {
			r, err := c.Receive(ctx)
			must(t, err)
			if story == "US-003" {
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
					trace.Events = append(trace.Events, storyObservation{Kind: "associated", ID: id, Parent: parent})
				}
				if v.Reason == "member-confirmed" && story == "US-011" {
					trace.Events = append(trace.Events, storyObservation{Kind: "associated", ID: id})
				}
				if strings.Contains(v.Reason, "unconfirmed") || strings.Contains(v.Reason, "unverified") || v.Reason == "forwarding-not-confirmed" {
					trace.Events = append(trace.Events, storyObservation{Kind: "gap"})
				}
			}
			if c.Unattributed().CoverageGap {
				trace.Events = append(trace.Events, storyObservation{Kind: "gap"})
			}
		}
	}
	if story == "US-011" {
		readUntil("snapshot")
		return
	}
	_, trace.SendError = c.Submit(ctx, claude.Input{Text: "start"})
	if story == "US-009" {
		must(t, c.Interrupt(ctx, claude.InterruptOptions{})) // control reply follows the complete unread burst
		readUntil("barrier")
		trace.OverflowError = c.Err()
		return
	}
	if story == "US-006" || story == "US-007" || story == "US-008" {
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
		if story != "US-006" {
			var questions clt.AskUserQuestionInput
			must(t, json.Unmarshal(r.Input(), &questions))
			require.Len(t, questions.Questions, 1, "question lost")
			text := "free text / 自由文本"
			updated, err := questions.WithAnswers(map[string]clt.QuestionAnswer{questions.Questions[0].Question: {Text: &text}})
			must(t, err)
			answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}
		}
		if story == "US-008" {
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
	if story == "US-003" {

		var forged claude.PermissionRequest
		assert.Error(t, forged.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny}), "zero permission handle answered")
	}
	if story == "US-012" {
		trace.InterruptError = c.Interrupt(ctx, claude.InterruptOptions{})
	}
	if story == "US-001" || story == "US-002" || story == "US-012" {
		_, err = c.Submit(ctx, claude.Input{Text: "release"})
		must(t, err)
		readUntil("end")
	}
	if story == "US-012" {
		trace.CloseError = c.Close()
	}
	return
}
func claudeFact(trace *storyTrace, v claude.Event) {
	add := func(kind, id, parent, raw string) {
		trace.Events = append(trace.Events, storyObservation{Kind: kind, ID: id, Parent: parent, Raw: raw})
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

// Retain the focused Codex US-014 entrypoint. The combined native browser and
// Claude local-query acceptance live in history_query_test.go.
func (s *CommonStoriesSuite) TestIndependentHistoryCodex() {
	s.Run("US-014/codex", func() { codexIndependentHistory(s.T()) })
}
