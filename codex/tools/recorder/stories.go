package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/codex"
	nativeReplay "github.com/zoumo/agent-sdk-go/codex/tools/replay"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
	recording "github.com/zoumo/agent-sdk-go/internal/recording"
)

func runStoryEntry() (bool, error) {
	return recording.StoryCommand("codex", nil, runCodexStory, codexStoryReplayPeer)
}

func codexStoryReplayPeer(p recording.StoryPlan) error {
	frames, e := recording.ReadFrames(filepath.Join(p.Raw, "raw.jsonl"))
	if e != nil {
		return e
	}
	converted := make([]nativeReplay.Frame, len(frames))
	for i, f := range frames {
		converted[i] = nativeReplay.Frame{Ordinal: f.Ordinal, Direction: f.Direction, Channel: f.Channel, Epoch: f.Epoch, ElapsedMS: f.ElapsedMS, Wire: f.Wire}
	}
	return nativeReplay.RunStoryPeer(context.Background(), os.Stdin, os.Stdout, converted)
}

func runCodexStory(ctx context.Context, p recording.StoryPlan, replay bool, r *recording.StoryReport) (err error) {
	if !replay {
		return errors.New("historical Go story driver is replay-only; use official Python SDK recorder")
	}
	id := strings.TrimPrefix(p.Story, "SDK-US-")
	if id == "006" || id == "007" {
		r.Status = "blocked"
		r.Gaps = []string{"recursive child attempt/depth/tool enforcement is not proven for this native version; no model launched"}
		return nil
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	mode := "--story-proxy"
	if replay {
		mode = "--story-peer"
	}
	c, e := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{mode, filepath.Join(p.Root, "plan.json")}, Dir: p.Project, Env: []string{"PATH=/usr/bin:/bin", "SDK_REAL_AGENT_E2E=1"}, Initialize: ct.InitializeParams{ClientInfo: ct.ClientInfo{Name: "per-story-recorder", Version: "1"}, Capabilities: ct.Some(ct.InitializeCapabilities{ExperimentalAPI: ct.Some(true)})}, Limits: codex.Limits{CallTimeout: 25 * time.Second, ShutdownTimeout: 4 * time.Second}})
	if e != nil {
		return e
	}
	defer func() {
		err = errors.Join(err, c.Close())
		if !replay {
			var t recording.TransportResult
			b, e := os.ReadFile(filepath.Join(p.Raw, "transport.json"))
			if e == nil {
				e = json.Unmarshal(b, &t)
			}
			if e != nil || !t.Reaped || !t.CleanupComplete {
				err = errors.Join(err, errors.New("native cleanup incomplete"))
			}
		}
	}()
	thread := ct.ThreadStartParams{Cwd: ct.Some(p.Project), Sandbox: ct.Some(ct.SandboxModeReadOnly), Ephemeral: ct.Some(id != "019"), BaseInstructions: ct.Some("Perform only the supplied synthetic task. No external context, files, shell, integrations, memories or additional agents. Use only specifically advertised fixture tools when explicitly requested."), DeveloperInstructions: ct.Some("Use only the isolated synthetic project and the supplied task.")}
	if p.Model != "" {
		thread.Model = ct.Some(p.Model)
	}
	if id == "004" {
		var tools []ct.DynamicToolSpec
		e = json.Unmarshal([]byte(`[{"type":"function","name":"fixture_read","description":"Read only synthetic numbers.txt once.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["numbers.txt"]}},"required":["path"],"additionalProperties":false}},{"type":"function","name":"fixture_write","description":"Write only synthetic summary.txt once with sum=42 and newline.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["summary.txt"]},"text":{"type":"string","enum":["sum=42\n"]}},"required":["path","text"],"additionalProperties":false}}]`), &tools)
		if e != nil {
			return e
		}
		thread.DynamicTools = ct.Some(tools)
	}
	s, e := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: thread})
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	model := p.Model
	if start, ok := s.StartResponse(); ok {
		if model == "" {
			model = start.Model
		}
		r.Receipts = append(r.Receipts, map[string]any{"operation": "thread/start", "nativeModel": start.Model, "nativeReasoningEffort": start.ReasoningEffort})
	}
	control := func() error {
		receipt, e := s.SetModel(ctx, model)
		r.Receipts = append(r.Receipts, map[string]any{"operation": "SetModel", "receipt": receipt, "rejected": e != nil})
		if e != nil {
			r.Gaps = append(r.Gaps, "native model control rejected")
		}
		receipt, e = s.SetThinking(ctx, codex.ThinkingChange{Effort: ct.Some(ct.ReasoningEffort("low"))})
		r.Receipts = append(r.Receipts, map[string]any{"operation": "SetThinking", "receipt": receipt, "rejected": e != nil})
		if e != nil {
			r.Gaps = append(r.Gaps, "native thinking control rejected")
		}
		return c.Err()
	}
	drain := func() error {
		for {
			_, e := s.Receive(ctx)
			if e != nil {
				if errors.Is(e, io.EOF) || errors.Is(e, codex.ErrClosed) {
					return nil
				}
				return e
			}
			r.Events++
		}
	}
	if id == "018" {
		if e = c.Close(); e != nil {
			return e
		}
		_, e = s.Submit(ctx, codex.Input{Input: []ct.UserInput{{Text: &ct.TextUserInput{Type: "text", Text: "must not be sent"}}}})
		if e == nil {
			return errors.New("disconnected session accepted input")
		}
		r.Facts = append(r.Facts, "application disconnected owned native process; subsequent Submit rejected without reconnect")
		r.Gaps = append(r.Gaps, "unexpected transport loss and recovery are separate fault coverage")
		return nil
	}
	if id == "008" {
		page, e := s.ListSubagents(ctx, codex.SubagentQuery{MaxPages: 2, MaxRecords: 20, MaxBytes: 1 << 20})
		r.Receipts = append(r.Receipts, page)
		if e != nil {
			r.Gaps = append(r.Gaps, "native descendant enumeration rejected or unavailable")
		}
		r.Gaps = append(r.Gaps, "fresh synthetic root has no previously created children; empty result is not that acceptance")
		return nil
	}
	if id == "015" {
		wait, stop := context.WithCancel(ctx)
		stop()
		_, e = s.Receive(wait)
		if !errors.Is(e, context.Canceled) {
			return errors.New("local Receive cancellation changed")
		}
		r.Facts = append(r.Facts, "local Receive wait canceled before continued successful Submit")
		r.Gaps = append(r.Gaps, "unknown dispatch outcome remains deterministic offline fault coverage")
	}
	if id == "012" {
		if e = control(); e != nil {
			return e
		}
	}
	if id == "009" {
		r.Status = "blocked"
		r.Gaps = append(r.Gaps, "native file approval envelope has no patch/path; exact safe approval cannot be proved; dynamic tools do not replace native approval")
		return nil
	}
	prompts := p.Prompts
	if id == "003" {
		prompts = prompts[:1]
	}
	fixtureRoot := p.Project
	if replay && id == "004" {
		scratch, e := os.MkdirTemp(p.Root, "replay-fixture-")
		if e != nil {
			return e
		}
		identity, e := os.Stat(scratch)
		if e != nil {
			return e
		}
		defer func() {
			current, e := os.Lstat(scratch)
			if e == nil && os.SameFile(identity, current) {
				err = errors.Join(err, os.RemoveAll(scratch))
			} else {
				err = errors.Join(err, errors.New("replay scratch ownership changed"))
			}
		}()
		if e = os.WriteFile(filepath.Join(scratch, "numbers.txt"), []byte("17\n25\n"), 0600); e != nil {
			return e
		}
		fixtureRoot = scratch
	}
	h := &nativeReplay.Handler{Root: fixtureRoot, Story: "none"}
	if replay {
		h.UsePrivateReplayPeer(c)
	}
	if id == "004" {
		h.Story = "s1"
	}
	if id == "010" || id == "011" {
		h.Story = "s2"
		prompts = []string{s2}
	}
	if id == "010" {
		h.HoldFirst = true
		h.WhileHeld = func(ctx context.Context) error {
			receipt, e := s.SetThinking(ctx, codex.ThinkingChange{})
			r.Receipts = append(r.Receipts, receipt)
			if e == nil {
				r.Facts = append(r.Facts, "original native question held during successful control")
			}
			return e
		}
		r.Gaps = append(r.Gaps, "question permission is not native file/command approval")
	}
	supplementary, interrupted := false, false
	h.BeforeSubmit = func() error {
		r.Inputs++
		if r.Inputs > 2 {
			return errors.New("story input budget exhausted")
		}
		return nil
	}
	h.Progress = func(ctx context.Context, event *ct.Event) error {
		r.Events++
		if id == "013" && event.Method == "thread/settings/updated" {
			r.Facts = append(r.Facts, "native settings observation retained separately from request receipt")
		}
		if event.Method != "turn/started" {
			return nil
		}
		var n ct.TurnStartedNotification
		if e := json.Unmarshal(event.Params, &n); e != nil {
			return e
		}
		if id == "003" && !supplementary {
			supplementary = true
			r.Inputs++
			sub, e := s.Submit(ctx, codex.Input{Input: []ct.UserInput{{Text: &ct.TextUserInput{Type: "text", Text: p.Prompts[1]}}}})
			r.Receipts = append(r.Receipts, map[string]any{"operation": "SubmitDuringExecution", "receipt": sub, "rejected": e != nil})
			if e == nil {
				r.Facts = append(r.Facts, "additional turn/start accepted after native execution start")
			} else {
				r.Facts = append(r.Facts, "native execution-time input rejection preserved without steer/retry")
			}
			return c.Err()
		}
		if id == "016" && !interrupted {
			e := s.Interrupt(ctx, codex.InterruptOptions{TurnID: n.Turn.ID})
			r.Receipts = append(r.Receipts, map[string]any{"operation": "Interrupt", "accepted": e == nil})
			if e != nil {
				return e
			}
			interrupted = true
			r.Facts = append(r.Facts, "native Interrupt issued against observed execution identity")
		}
		return nil
	}
	for turn, prompt := range prompts {
		params := ct.TurnStartParams{Input: []ct.UserInput{{Text: &ct.TextUserInput{Type: "text", Text: prompt}}}}
		if id == "002" {
			params.Input = append(params.Input, ct.UserInput{Text: &ct.TextUserInput{Type: "text", Text: "Use no tools."}})
			r.Facts = append(r.Facts, "native structured input array preserves two separate text blocks")
			r.Gaps = append(r.Gaps, "image and attachment inputs are separate missing cases")
		}
		if id == "014" {
			h.Progress = func(ctx context.Context, event *ct.Event) error { r.Events++; return nil }
			// Submit first, issue controls before consuming Session events, then receive
			// using RunSession is not possible without submitting twice, so use the native loop.
			r.Inputs++
			if _, e = s.Submit(ctx, codex.Input{Input: params.Input}); e != nil {
				return e
			}
			if e = control(); e != nil {
				return e
			}
			r.Facts = append(r.Facts, "native controls returned before Session.Receive consumption")
			for {
				v, e := s.Receive(ctx)
				if e != nil {
					return e
				}
				r.Events++
				if v.Request != nil {
					return errors.New("unexpected request in slow-consumer scene")
				}
				if v.Event != nil && v.Event.Method == "turn/completed" {
					r.Results++
					break
				}
			}
			r.Gaps = append(r.Gaps, "finite slow-consumer sample, not an unbounded queue stress proof")
			break
		}
		result, e := nativeReplay.RunSession(ctx, s, params, h)
		r.Receipts = append(r.Receipts, result)
		if e != nil {
			return e
		}
		r.Results++
		if result.Terminal.Status != ct.TurnStatusCompleted && !(id == "016" && result.Terminal.Status == ct.TurnStatusInterrupted) {
			return errors.New("native root execution failed")
		}
		if id == "013" && turn == 0 {
			if e = control(); e != nil {
				return e
			}
		}
	}
	r.Requests = h.Calls + h.Questions
	if id == "004" && h.Calls != 2 {
		r.Status = "untriggered"
		r.Gaps = append(r.Gaps, "two exact dynamic fixture tools were not both observed")
	}
	if (id == "010" || id == "011") && h.Questions != 1 {
		r.Status = "untriggered"
		r.Gaps = append(r.Gaps, "original native question was not observed")
	}
	if id == "003" {
		r.Gaps = append(r.Gaps, "this sample retains execution-time submission; all possible native scheduling outcomes are not claimed")
	}
	if id == "016" && !interrupted {
		r.Status = "untriggered"
		r.Gaps = append(r.Gaps, "interrupt barrier did not occur")
	}
	if id == "005" {
		r.Facts = append(r.Facts, "original native envelopes, unknown members and source order retained")
		r.Gaps = append(r.Gaps, "deliberate unknown/duplicate mutations remain labeled synthetic")
	}
	if id == "017" {
		if e = s.Close(); e != nil {
			return e
		}
		if e = drain(); e != nil {
			return e
		}
		r.Facts = append(r.Facts, "accepted Session events drained after Close")
		r.Gaps = append(r.Gaps, "native task failure and transport faults remain separate evidence")
	}
	if id == "019" {
		if e = s.Close(); e != nil {
			return e
		}
		query, e := nativeReplay.QuerySavedRoot(ctx, c, p.Project, s.ThreadID())
		r.Receipts = append(r.Receipts, query)
		if e != nil {
			r.Gaps = append(r.Gaps, "bounded independent saved-root query unsupported or incomplete at this version")
		} else {
			r.Facts = append(r.Facts, "independent metadata/turn/item queries read only this fresh synthetic root without resume")
		}
	}
	if len(r.Gaps) == 0 && r.Status != "untriggered" {
		r.Status = "captured"
	}
	return nil
}
