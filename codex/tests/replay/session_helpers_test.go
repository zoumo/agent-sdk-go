package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func equal(a, b any) bool { return reflect.DeepEqual(a, b) }
func canonical(b []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&v)
	return v
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Handler exposes two bounded operations on synthetic data only. Paths must be
// exact basenames, regular files, and resolve into the isolated root. No shell.
type Handler struct {
	Root      string
	Scenario  string
	Calls     int
	Questions int
	// Exact is a finite ordered policy for native root requests, separate from synthetic tool/question scenarios.
	Exact        []ExactRequest
	Answered     int
	BeforeSubmit func() error
	HoldFirst    bool
	WhileHeld    func(context.Context) error
	heldOnce     bool
	Progress     func(context.Context, *types.Event) error
	// Set only by explicit offline replay; ordinary SDK fixture peers never use the
	// offline peer's private synchronization protocol.
	replayClient *codex.Client
}

func (h *Handler) Answer(ctx context.Context, r codex.Request) error {
	if h.Exact != nil {
		return h.answerExact(ctx, r)
	}
	switch r.Method() {
	case "item/tool/call":
		var p struct {
			Tool      string `json:"tool"`
			Arguments struct {
				Path string `json:"path"`
				Text string `json:"text"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params(), &p); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(r.Params(), &fields); err != nil {
			return err
		}
		for key := range fields {
			switch key {
			case "threadId", "turnId", "callId", "namespace", "tool", "arguments":
			default:
				return errors.Join(errors.New("unknown dynamic tool request field"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"}))
			}
		}
		var arguments map[string]json.RawMessage
		if err := json.Unmarshal(fields["arguments"], &arguments); err != nil {
			return err
		}
		allowedArguments := (p.Tool == "fixture_read" && len(arguments) == 1 && arguments["path"] != nil) || (p.Tool == "fixture_write" && len(arguments) == 2 && arguments["path"] != nil && arguments["text"] != nil)
		namespace := fields["namespace"]
		if !allowedArguments || (namespace != nil && string(namespace) != "null") {
			return errors.Join(errors.New("out-of-policy dynamic tool arguments/namespace"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"}))
		}
		if h.Scenario != "dynamic-tools" || h.Calls >= 2 {
			return errors.Join(errors.New("recording policy violation; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"}))
		}
		var text string
		var err error
		switch {
		case p.Tool == "fixture_read" && p.Arguments.Path == "numbers.txt" && h.Calls == 0:
			var b []byte
			b, err = readSynthetic(h.Root)
			text = string(b)
		case p.Tool == "fixture_write" && p.Arguments.Path == "summary.txt" && p.Arguments.Text == "sum=42\n" && h.Calls == 1:
			err = writeSynthetic(h.Root, p.Arguments.Text)
			text = "written summary.txt"
		default:
			return errors.Join(errors.New("recording policy violation; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "operation denied"}))
		}
		if err != nil {
			return err
		}
		h.Calls++
		return r.Handle().Answer(ctx, map[string]any{"contentItems": []any{map[string]any{"type": "inputText", "text": text}}, "success": true})
	case "item/tool/requestUserInput":
		var p types.ToolRequestUserInputParams
		if err := json.Unmarshal(r.Params(), &p); err != nil {
			return err
		}
		if h.Scenario != "question" || h.Questions != 0 || len(p.Questions) != 1 {
			return errors.Join(errors.New("recording policy violation; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "question denied"}))
		}
		q := p.Questions[0]
		options, ok := q.Options.Get()
		if !ok || len(options) != 2 || options[0].Label != "Amber" || options[1].Label != "Indigo" || q.IsSecret.ValueOr(false) {
			return errors.Join(errors.New("recording policy violation; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "question denied"}))
		}
		h.Questions++
		return r.Handle().Answer(ctx, types.ToolRequestUserInputResponse{Answers: map[string]types.ToolRequestUserInputAnswer{q.ID: {Answers: []string{"Amber"}}}})
	default:
		return errors.Join(errors.New("recording policy violation; stop scene"), r.Handle().Reject(ctx, &codex.RPCError{Code: -32601, Message: "request denied"}))
	}
}

type Observation struct {
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"`
	Method   string `json:"method,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}
type Result struct {
	Observations []Observation `json:"observations"`
	Tools        int           `json:"tools"`
	Questions    int           `json:"questions"`
	Completed    bool          `json:"completed"`
	Terminal     types.Turn    `json:"terminal"`
}

// Session consumes native facts and real requests through the Session FIFO.
func RunSession(ctx context.Context, session *codex.Session, turn types.TurnStartParams, h *Handler) (Result, error) {
	var result Result
	if h.BeforeSubmit != nil {
		if err := h.BeforeSubmit(); err != nil {
			return result, err
		}
	}
	if len(turn.UnknownFields) != 0 {
		return result, fmt.Errorf("%w: replay turn execution extensions are unsupported", codex.ErrInvalidInput)
	}
	input := codex.Input{
		AdditionalContext:          turn.AdditionalContext,
		ApprovalPolicy:             turn.ApprovalPolicy,
		ApprovalsReviewer:          turn.ApprovalsReviewer,
		ClientUserMessageID:        turn.ClientUserMessageID,
		CollaborationMode:          turn.CollaborationMode,
		Cwd:                        turn.Cwd,
		CyberAccessProgram:         turn.CyberAccessProgram,
		DisabledPluginIDs:          turn.DisabledPluginIDs,
		Effort:                     turn.Effort,
		Environments:               turn.Environments,
		Input:                      turn.Input,
		Model:                      turn.Model,
		MultiAgentMode:             turn.MultiAgentMode,
		OutputSchema:               turn.OutputSchema,
		ParentTurnID:               turn.ParentTurnID,
		Permissions:                turn.Permissions,
		Personality:                turn.Personality,
		ResponsesapiClientMetadata: turn.ResponsesapiClientMetadata,
		RootTurnID:                 turn.RootTurnID,
		RuntimeWorkspaceRoots:      turn.RuntimeWorkspaceRoots,
		SandboxPolicy:              turn.SandboxPolicy,
		ServiceTier:                turn.ServiceTier,
		ServiceTierForTurn:         turn.ServiceTierForTurn,
		Summary:                    turn.Summary,
		ToolOutput:                 turn.ToolOutput,
		TurnTrigger:                turn.TurnTrigger,
	}
	receiveCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	type submission struct {
		value codex.Submission
		err   error
	}
	submitted := make(chan submission, 1)
	var submittedTurnID string
	go func() {
		value, err := session.Submit(receiveCtx, input)
		submitted <- submission{value: value, err: err}
		if err != nil {
			cancel(err)
		}
	}()
	submissionIdentity := func() error {
		if submittedTurnID != "" {
			return nil
		}
		select {
		case sub := <-submitted:
			if sub.err != nil {
				return sub.err
			}
			submittedTurnID = sub.value.Response.Turn.ID
			if submittedTurnID == "" {
				return errors.New("submission missing native turn identity")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var held *codex.Request
	answer := func(request codex.Request) error {
		answerCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		if err := h.Answer(answerCtx, request); err != nil {
			return err
		}
		if h.replayClient != nil {
			return h.replayClient.Call(ctx, "replay/answer", nil, nil)
		}
		return nil
	}
	for !result.Completed {
		received, err := session.Receive(receiveCtx)
		if err != nil {
			if cause := context.Cause(receiveCtx); cause != nil {
				return result, cause
			}
			return result, err
		}
		if received.Request != nil {
			var identity struct {
				ThreadID string `json:"threadId"`
			}
			if json.Unmarshal(received.Request.Params(), &identity) != nil || identity.ThreadID != session.ThreadID() {
				return result, errors.New("non-root request in root-only scene")
			}
			if held != nil {
				return result, errors.Join(errors.New("additional request while exact request held"), received.Request.Handle().Reject(ctx, &codex.RPCError{Code: -32602, Message: "request denied"}))
			}
			if h.HoldFirst && !h.heldOnce {
				request := received.Request.Request
				held = &request
				h.heldOnce = true
				if h.WhileHeld == nil {
					return result, errors.New("deferred request requires bounded non-generating control")
				}
				if err := h.WhileHeld(ctx); err != nil {
					return result, err
				}
			} else if err := answer(received.Request.Request); err != nil {
				return result, err
			}
			continue
		}
		e := types.Event{}
		if received.Event != nil {
			e = *received.Event
		}
		o := Observation{Sequence: uint64(len(result.Observations) + 1), Kind: "Native"}
		if received.Event != nil {
			if e.DecodeError != nil {
				return result, errors.New("native decode contract")
			}
			raw, parseErr := object(e.Raw)
			if parseErr != nil || string(raw["method"]) != string(mustJSON(e.Method)) || !equal(canonical(raw["params"]), canonical(e.Params)) {
				return result, errors.New("Raw/Params contract")
			}
			var params map[string]json.RawMessage
			_ = json.Unmarshal(e.Params, &params)
			_ = json.Unmarshal(params["threadId"], &o.ThreadID)
			if o.ThreadID != "" && o.ThreadID != session.ThreadID() {
				return result, errors.New("non-root event in root-only scene")
			}
			switch e.Method {
			case "turn/started", "turn/completed", "item/started", "item/completed", "item/agentMessage/delta", "serverRequest/resolved":
				if e.Data == nil {
					return result, errors.New("missing typed native data")
				}
			}
			if held != nil {
				if e.Method == "turn/completed" {
					return result, errors.New("deferred request progress untriggered before terminal")
				}
				if o.ThreadID == session.ThreadID() {
					if err := answer(*held); err != nil {
						return result, err
					}
					held = nil
				}
			}
			o.Method = e.Method
			progressMatches := true
			if h.Progress != nil && e.Method == "turn/started" {
				if err := submissionIdentity(); err != nil {
					return result, err
				}
				var started types.TurnStartedNotification
				if err := json.Unmarshal(e.Params, &started); err != nil {
					return result, err
				}
				progressMatches = started.Turn.ID == submittedTurnID
			}
			if h.Progress != nil && progressMatches {
				if err := h.Progress(ctx, &e); err != nil {
					return result, err
				}
			}
			if o.Method == "turn/completed" {
				if err := submissionIdentity(); err != nil {
					return result, err
				}
				var terminal types.TurnCompletedNotification
				if err := json.Unmarshal(e.Params, &terminal); err != nil {
					return result, err
				}
				if terminal.Turn.ID == submittedTurnID {
					result.Completed = true
					result.Terminal = terminal.Turn
				}
			}
		} else {
			if received.Observation == nil {
				return result, errors.New("unexpected convenience event in root-only story")
			}
			o.Kind = "ObservationState"
		}
		result.Observations = append(result.Observations, o)
	}

	result.Tools = h.Calls
	result.Questions = h.Questions
	return result, nil
}

func readSynthetic(root string) ([]byte, error) {
	p := root + "/numbers.txt"
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not regular")
	}
	b, err := os.ReadFile(p)
	if err == nil && string(b) != "17\n25\n" {
		err = errors.New("input changed")
	}
	return b, err
}
func writeSynthetic(root, text string) error {
	f, err := os.OpenFile(root+"/summary.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(text)
	return errors.Join(err, f.Close())
}
