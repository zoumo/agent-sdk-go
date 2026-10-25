package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// ExactRequest is one reviewed native request/answer pair. Params includes the
// complete original question/permission and identities; no wildcard matching.
// A dynamic tool response is never accepted by this native policy.
type ExactRequest struct {
	Method string
	Params json.RawMessage
	Result json.RawMessage
}

func (h *Handler) answerExact(ctx context.Context, r codex.Request) error {
	deny := func() error {
		return errors.Join(errors.New("out-of-policy native request; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "request denied"}))
	}
	if h.Answered >= len(h.Exact) {
		return deny()
	}
	p := h.Exact[h.Answered]
	if r.Method() != p.Method || !json.Valid(p.Params) || !equal(canonical(r.Params()), canonical(p.Params)) {
		return deny()
	}
	switch p.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		// No shell or write approval is safe in Phase 1. File approval contains no
		// patch/path, so it cannot prove exact content even with an exact envelope.
		if !equal(canonical(p.Result), canonical([]byte(`{"decision":"decline"}`))) {
			return deny()
		}
	case "item/permissions/requestApproval":
		if !equal(canonical(p.Result), canonical([]byte(`{"permissions":{},"scope":"turn"}`))) {
			return deny()
		}
	case "item/tool/requestUserInput":
		var params types.ToolRequestUserInputParams
		var answer types.ToolRequestUserInputResponse
		if json.Unmarshal(p.Params, &params) != nil || json.Unmarshal(p.Result, &answer) != nil || len(params.Questions) != 2 || len(answer.Answers) != 2 || len(params.UnknownFields) != 0 || len(answer.UnknownFields) != 0 {
			return deny()
		}
		ids := map[string]bool{}
		for i, q := range params.Questions {
			if ids[q.ID] {
				return deny()
			}
			ids[q.ID] = true
			a, ok := answer.Answers[q.ID]
			if !ok || q.ID == "" || q.IsSecret.ValueOr(false) || len(a.Answers) != 1 || len(a.UnknownFields) != 0 {
				return deny()
			}
			if i == 0 {
				opts, ok := q.Options.Get()
				if !ok || len(opts) != 2 || opts[0].Label != "Blue" || opts[1].Label != "Green" || a.Answers[0] != "Blue" {
					return deny()
				}
			} else if a.Answers[0] != "sample" {
				return deny()
			}
		}
	default:
		return deny()
	}
	if !json.Valid(p.Result) {
		return deny()
	}
	if err := r.Handle().Answer(ctx, p.Result); err != nil {
		return err
	}
	h.Answered++
	return nil
}

// RootPlan selects only the crosswalk's neutral root bundles. Media and native
// file-read approval are gaps at the pinned protocol; no shell is enabled to
// manufacture them. Reserve is called before every submitted input, including
// execution-time input. Offline callers may supply a synthetic accounting hook.
type RootPlan struct {
	Scene   string
	Model   string
	Effort  types.ReasoningEffort
	Reserve func() error
}
type RootResult struct {
	Turns             []Result
	Model             codex.ConfigurationReceipt
	Thinking          codex.ConfigurationReceipt
	Interleaved       *codex.Submission
	InterruptAccepted bool
	Untriggered       bool
}

const readyPrompt = "Reply with the word Ready."
const donePrompt = "Reply with the word Done."

func neutralTurn(text string) types.TurnStartParams {
	return types.TurnStartParams{Input: []types.UserInput{{Text: &types.TextUserInput{Type: "text", Text: text}}}, Environments: types.Some([]types.TurnEnvironmentParams{})}
}

// RunRoot uses an already opened Session; callers own Close and transport drain.
// Native acceptance receipts are retained separately from Receive observations.
// This helper neither starts a process nor grants live execution authority.
func RunRoot(ctx context.Context, s *codex.Session, p RootPlan, h *Handler) (out RootResult, err error) {
	if p.Scene != "r1" && p.Scene != "r2" && p.Scene != "r3" {
		return out, errors.New("unsupported root scene; query and children require separate paths")
	}
	if s == nil || h == nil || p.Reserve == nil {
		return out, errors.New("session, policy and attempt reservation required")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	if p.Scene == "r1" && (p.Model == "" || p.Effort == "") {
		return out, errors.New("explicit native model/effort required")
	}
	if p.Scene == "r2" && h.Exact == nil {
		return out, errors.New("reviewed exact native request policy required")
	}
	oldBefore, oldProgress := h.BeforeSubmit, h.Progress
	defer func() { h.BeforeSubmit = oldBefore; h.Progress = oldProgress }()
	h.BeforeSubmit = p.Reserve
	prompts := []string{readyPrompt, donePrompt}
	if p.Scene == "r2" {
		prompts = []string{"Read numbers.txt, then write summary.txt with total=18.", "Ask two questions once: choose Blue or Green; enter a short label."}
	}
	if p.Scene == "r3" {
		prompts = []string{"Produce a short numbered list from one to ten."}
		out.Untriggered = true
		h.Progress = func(ctx context.Context, e *types.Event) error {
			if !out.Untriggered || e.Method != "turn/started" {
				return nil
			}
			var n types.TurnStartedNotification
			if err := json.Unmarshal(e.Params, &n); err != nil {
				return err
			}
			if n.ThreadID != s.ThreadID() || n.Turn.ID == "" {
				return errors.New("invalid root execution barrier")
			}
			out.Untriggered = false
			if err := p.Reserve(); err != nil {
				return err
			}
			sub, err := s.Submit(ctx, codex.Input{Input: neutralTurn("Also include the word sample.").Input, Environments: types.Some([]types.TurnEnvironmentParams{})})
			out.Interleaved = &sub
			if err != nil {
				return err
			}
			err = s.Interrupt(ctx, codex.InterruptOptions{TurnID: n.Turn.ID})
			out.InterruptAccepted = err == nil
			return err
		}
	}
	for i, prompt := range prompts {
		turn := neutralTurn(prompt)
		// Output schema is structured input supported by the pinned turn protocol;
		// media remains separately gated. Do not map effort to a token budget.
		if p.Scene == "r1" && i == 0 {
			turn.OutputSchema = types.Some(json.RawMessage(`{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}`))
		}
		result, err := RunSession(ctx, s, turn, h)
		out.Turns = append(out.Turns, result)
		if err != nil {
			return out, err
		}
		if p.Scene == "r1" && i == 0 {
			out.Model, err = s.SetModel(ctx, p.Model)
			if err != nil {
				return out, err
			}
			out.Thinking, err = s.SetThinking(ctx, codex.ThinkingChange{Effort: types.Some(p.Effort)})
			if err != nil {
				return out, err
			}
		}
	}
	if p.Scene == "r2" && h.Answered != len(h.Exact) {
		out.Untriggered = true
		return out, fmt.Errorf("native request policy not fully triggered: %d/%d", h.Answered, len(h.Exact))
	}
	return out, nil
}
