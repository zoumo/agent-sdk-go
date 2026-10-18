package stories_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	claude "github.com/zoumo/agent-sdk-go/claude"
	clt "github.com/zoumo/agent-sdk-go/claude/types"
	codex "github.com/zoumo/agent-sdk-go/codex"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/testkit"
)

func TestCommonStories(t *testing.T) {
	for _, story := range testkit.Stories {
		t.Run(story, func(t *testing.T) {
			t.Run("codex", func(t *testing.T) { testkit.Check(t, story, codexStory(t, story)) })
			t.Run("claude", func(t *testing.T) { testkit.Check(t, story, claudeStory(t, story)) })
			if story == "US-006" || story == "US-008" {
				suffix := "-allow"
				if story == "US-008" {
					suffix = "-close"
				}
				t.Run("codex"+suffix, func(t *testing.T) { testkit.Check(t, story+suffix, codexStory(t, story+suffix)) })
				t.Run("claude"+suffix, func(t *testing.T) { testkit.Check(t, story+suffix, claudeStory(t, story+suffix)) })
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
	if err != nil {
		t.Fatal(err)
	}
}
func wait(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// Adapters deliberately retain native clients, native types and original answer
// handles locally. Only detached facts cross into the common assertion package.
func codexStory(t *testing.T, story string) (trace testkit.Trace) {
	mode := story
	story = strings.TrimSuffix(strings.TrimSuffix(story, "-allow"), "-close")
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{}, Env: []string{"SDK_STORY_PEER=codex", "SDK_STORY=" + story, "GORACE=atexit_sleep_ms=0"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "stories", Version: "synthetic"}}})
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })
	opts := ct.SubscriptionOptions{PendingBytes: 1}
	if story == "US-009" {
		opts.EventQueue = 2
	}
	if story == "US-011" {
		opts.PendingBytes = 4096
		opts.History = []json.RawMessage{json.RawMessage(`{"id":"child","parentThreadId":"root"}`)}
	}
	s, err := c.Subscribe(ctx, "root", opts)
	must(t, err)
	defer s.Close()
	readUntil := func(target string) {
		for {
			select {
			case e, ok := <-s.Events():
				if !ok {
					t.Fatalf("observation ended: %v", s.Err())
				}
				codexFact(&trace, e)
				if e.Method == "fixture/"+target {
					return
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	if story == "US-011" {
		must(t, c.Call(ctx, "fixture/snapshot", nil, nil))
		readUntil("snapshot")
		return
	}
	_, trace.SendError = c.StartTurn(ctx, ct.TurnStartParams{ThreadID: "root", Input: []ct.UserInput{}})
	if story == "US-009" {
		wait(t, ctx, s.Done())
		trace.OverflowError = s.Err()
		if !errors.Is(trace.OverflowError, codex.ErrSubscriptionOverflow) {
			t.Fatalf("wrong overflow: %v", trace.OverflowError)
		}
		return
	}
	if story == "US-006" || story == "US-007" || story == "US-008" {
		var r codex.Request
		select {
		case r = <-c.Requests():
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if r.ID().String() == "" {
			t.Fatal("missing native request identity")
		}
		_, err := r.Data()
		must(t, err)
		if strings.HasSuffix(mode, "-close") {
			trace.CloseError = c.Close()
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
			if len(questions.Questions) != 1 || questions.Questions[0].ID != "q" {
				t.Fatal("question identity lost")
			}
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
		select {
		case <-c.Requests():
			t.Fatal("relationship minted answer authority")
		default:
		}
		var forged codex.AnswerHandle
		if forged.Answer(ctx, nil) == nil {
			t.Fatal("zero handle answered")
		}
	}
	if story == "US-012" {
		_, trace.InterruptError = c.InterruptTurn(ctx, ct.TurnInterruptParams{ThreadID: "root", TurnID: "turn"})
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
func codexFact(trace *testkit.Trace, e ct.Event) {
	add := func(kind, id, parent string) {
		trace.Events = append(trace.Events, testkit.Observation{Kind: kind, ID: id, Parent: parent, Raw: string(e.Raw)})
	}
	switch v := e.Data.(type) {
	case *ct.SubagentAssociated:
		add("associated", v.ThreadID, v.ParentThreadID)
	case *ct.SubagentCreated:
		add("created", v.ThreadID, v.ParentThreadID)
	case *ct.ObservationState:
		if v.State == "incomplete" || strings.Contains(v.Reason, "unresolved") {
			add("gap", v.UnresolvedThreadID, "")
		}
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

func claudeStory(t *testing.T, story string) (trace testkit.Trace) {
	mode := story
	story = strings.TrimSuffix(strings.TrimSuffix(story, "-allow"), "-close")
	ctx := deadline(t)
	exe, err := os.Executable()
	must(t, err)
	dir := t.TempDir()
	requests := make(chan *claude.PermissionRequest, 4)
	opts := claude.Options{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, "SDK_STORY_PEER=claude", "SDK_STORY=" + story, "GORACE=atexit_sleep_ms=0"}, PermissionHandler: func(r *claude.PermissionRequest) { requests <- r }, Subagents: claude.SubagentOptions{MaxUnresolvedBytes: 1}}
	if story == "US-009" {
		opts.Limits.EventBuffer = 2
	}
	if story == "US-011" {
		opts.ResumeID = "s"
		opts.Subagents.History = []claude.SubagentHistory{{SessionID: "s", TaskID: "child"}}
	}
	c, err := claude.Open(ctx, opts)
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })
	readUntil := func(target string) {
		for {
			e, err := c.Receive(ctx)
			must(t, err)
			claudeFact(&trace, e)
			if n, ok := e.(claude.Event); ok && n.Type == "fixture_"+target {
				return
			}
		}
	}
	if story == "US-011" {
		must(t, c.Send(ctx, "snapshot"))
		readUntil("snapshot")
		return
	}
	trace.SendError = c.Send(ctx, "start")
	if story == "US-009" {
		wait(t, ctx, c.Done())
		trace.OverflowError = c.Err()
		var overload *claude.ResourceLimitError
		if !errors.As(trace.OverflowError, &overload) {
			t.Fatalf("wrong overflow: %v", trace.OverflowError)
		}
		return
	}
	if story == "US-006" || story == "US-007" || story == "US-008" {
		var r *claude.PermissionRequest
		select {
		case r = <-requests:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if r.RequestID() != "original" {
			t.Fatal("request ID changed")
		}
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
			if len(questions.Questions) != 1 {
				t.Fatal("question lost")
			}
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
		select {
		case <-requests:
			t.Fatal("relationship minted permission authority")
		default:
		}
		var forged claude.PermissionRequest
		if forged.Answer(ctx, claude.PermissionAnswer{Behavior: claude.PermissionDeny}) == nil {
			t.Fatal("zero permission handle answered")
		}
	}
	if story == "US-012" {
		trace.InterruptError = c.Interrupt(ctx)
	}
	if story == "US-001" || story == "US-002" || story == "US-012" {
		must(t, c.Send(ctx, "release"))
		readUntil("end")
	}
	if story == "US-012" {
		trace.CloseError = c.Close()
	}
	return
}
func claudeFact(trace *testkit.Trace, event claude.ReceivedEvent) {
	add := func(kind, id, parent, raw string) {
		trace.Events = append(trace.Events, testkit.Observation{Kind: kind, ID: id, Parent: parent, Raw: raw})
	}
	identity := func(i claude.SubagentIdentity) string {
		if i.TaskID != "" {
			return i.TaskID
		}
		if i.AgentID != "" {
			return i.AgentID
		}
		return "root"
	}
	switch v := event.(type) {
	case claude.SubagentAssociated:
		parent := ""
		if v.Parent != nil {
			parent = identity(*v.Parent)
		}
		add("associated", identity(v.Source), parent, "")
	case claude.SubagentCreated:
		parent := ""
		if v.Parent != nil {
			parent = identity(*v.Parent)
		}
		add("created", identity(v.Source), parent, "")
	case claude.ObservationState:
		if v.State == "incomplete" || v.Scope == "unresolved" {
			add("gap", v.Source.AgentID, "", "")
		}
	case claude.Event:
		switch v.Type {
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
}
