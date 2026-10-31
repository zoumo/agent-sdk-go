package claudesim

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/claude/types"
)

// AcceptFlow executes recorded operations with the public Go Session. Incoming
// permissions retain their original handles, including held approvals.
func AcceptFlow(ctx context.Context, launcher, root string, deliveries io.Writer) (err error) {
	var plan struct {
		Project, Config, Model, DriverProfile string
		Args                                  []string
	}
	b, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &plan); err != nil {
		return err
	}
	type frame struct {
		Ordinal   int
		Direction string
		Wire      json.RawMessage
	}
	type control struct {
		Type, Subtype string
		RequestID     string `json:"request_id"`
		SessionID     string `json:"session_id"`
		Request       json.RawMessage
		Response      struct {
			Subtype   string
			RequestID string `json:"request_id"`
			Response  json.RawMessage
		}
	}
	f, err := os.Open(filepath.Join(root, "raw/raw.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var frames []frame
	var init types.InitializeOptions
	answers := map[string]json.RawMessage{}
	nativeRequests := map[string]json.RawMessage{}
	var servers []string
	for scanner.Scan() {
		var v frame
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			return err
		}
		frames = append(frames, v)
		var w control
		if err := json.Unmarshal(v.Wire, &w); err != nil {
			return err
		}
		if v.Direction == "c2s" && w.Type == "control_request" {
			var r struct{ Subtype string }
			_ = json.Unmarshal(w.Request, &r)
			if r.Subtype == "initialize" {
				if err := json.Unmarshal(w.Request, &init); err != nil {
					return err
				}
			}
		}
		if v.Direction == "c2s" && w.Type == "control_response" {
			answers[w.Response.RequestID] = w.Response.Response
		}
		if v.Direction == "s2c" && w.Type == "control_request" {
			nativeRequests[w.RequestID] = v.Wire
			var r struct{ Subtype string }
			_ = json.Unmarshal(w.Request, &r)
			if r.Subtype == "mcp_message" {
				var metadata struct {
					ServerName string `json:"server_name"`
				}
				_ = json.Unmarshal(w.Request, &metadata)
				found := false
				for _, s := range servers {
					found = found || s == metadata.ServerName
				}
				if !found {
					servers = append(servers, metadata.ServerName)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	var mu sync.Mutex
	seen := map[string]int{}
	consumed := map[string]int{}
	requests := map[string]*claude.IncomingRequest{}
	changed := make(chan struct{}, 1)
	done := make(chan struct{})
	var receiveErr error
	key := func(raw json.RawMessage) string {
		var v any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		_ = decoder.Decode(&v)
		b, _ := json.Marshal(v)
		return string(b)
	}
	encoder := json.NewEncoder(deliveries)
	observe := func(got claude.Received) error {
		mu.Lock()
		defer mu.Unlock()
		if err := encoder.Encode(struct {
			Kind        claude.ReceivedKind     `json:"kind"`
			Raw         string                  `json:"raw,omitempty"`
			Source      claude.SubagentIdentity `json:"source"`
			Origin      string                  `json:"origin"`
			Observation *claude.Observation     `json:"observation,omitempty"`
		}{got.Kind, string(got.Raw), got.Source, got.Origin, got.Observation}); err != nil {
			return err
		}
		if len(got.Raw) > 0 {
			seen[key(got.Raw)]++
		}
		if got.Request != nil {
			requests[got.Request.ID()] = got.Request
		}
		select {
		case changed <- struct{}{}:
		default:
		}
		return nil
	}
	options := claude.SessionOptions{CLIPath: launcher, WorkDir: plan.Project, Model: plan.Model, Env: []string{"CLAUDE_CONFIG_DIR=" + plan.Config}, Initialize: &init, SDKMCPServers: servers}
	for i := 0; i < len(plan.Args); i++ {
		name, value, inline := strings.Cut(plan.Args[i], "=")
		if name != "--model" && name != "--effort" && name != "--max-thinking-tokens" {
			continue
		}
		if !inline {
			i++
			if i == len(plan.Args) {
				return errors.New("missing creation option value")
			}
			value = plan.Args[i]
		}
		switch name {
		case "--model":
			options.Model = value
		case "--effort":
			options.Effort = value
		case "--max-thinking-tokens":
			budget, e := strconv.Atoi(value)
			if e != nil {
				return e
			}
			options.Thinking = &types.ThinkingConfig{Type: "enabled", BudgetTokens: &budget}
		}
	}
	if len(servers) > 0 {
		options.HandleSDKMCP = func(ctx context.Context, r claude.IncomingRequest) error {
			if !sameJSON(r.Raw(), nativeRequests[r.ID()]) {
				return errors.New("MCP request differs from recording")
			}
			var answer struct {
				MCPResponse json.RawMessage `json:"mcp_response"`
			}
			if err := json.Unmarshal(answers[r.ID()], &answer); err != nil {
				return err
			}
			if err := observe(claude.Received{Kind: claude.ReceivedRequest, Request: &r, Raw: r.Raw(), Origin: "live"}); err != nil {
				return err
			}
			return r.ReplyMCP(ctx, answer.MCPResponse)
		}
	}
	s, err := claude.Open(ctx, options)
	if err != nil {
		return err
	}

	go func() {
		defer close(done)
		for {
			got, e := s.Receive(ctx)
			if e == nil {
				e = observe(got)
			}
			if e != nil {
				mu.Lock()
				receiveErr = e
				mu.Unlock()
				return
			}
		}
	}()
	defer func() { err = errors.Join(err, s.Close()); <-done }()
	wait := func(raw json.RawMessage) error {
		for {
			mu.Lock()
			k := key(raw)
			ok := seen[k] > consumed[k]
			if ok {
				consumed[k]++
			}
			e := receiveErr
			mu.Unlock()
			if ok {
				return nil
			}
			if e != nil {
				return e
			}
			select {
			case <-changed:
			case <-done:
				return errors.New("Session ended before expected native fact")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	submitted, terminals := 0, 0
	observedRoot := ""
	for _, v := range frames {
		var w control
		if err := json.Unmarshal(v.Wire, &w); err != nil {
			return err
		}
		if v.Direction == "s2c" {
			if w.Type == "control_response" {
				continue
			}
			if w.Type == "control_request" {
				if err := wait(v.Wire); err != nil {
					return fmt.Errorf("ordinal %d request: %w", v.Ordinal, err)
				}
				continue
			}
			var native struct {
				SessionID     string `json:"session_id"`
				Type, Subtype string
			}
			_ = json.Unmarshal(v.Wire, &native)
			if native.Type == "system" && native.Subtype == "init" {
				observedRoot = native.SessionID
			}
			if observedRoot != "" && native.SessionID == observedRoot {
				if err := wait(v.Wire); err != nil {
					return fmt.Errorf("ordinal %d %s/%s: %w", v.Ordinal, native.Type, native.Subtype, err)
				}
				if w.Type == "result" {
					terminals++
				}
			}
			continue
		}
		switch w.Type {
		case "user":
			var input types.UserInput
			if err := json.Unmarshal(v.Wire, &input); err != nil {
				return err
			}
			sub, e := s.Submit(ctx, claude.Input{User: &input})
			if e != nil {
				return e
			}
			if !sub.Written {
				return errors.New("input not written")
			}
			submitted++
		case "control_request":
			var request struct {
				Subtype, Mode     string
				Model             *string
				MaxThinkingTokens *int    `json:"max_thinking_tokens"`
				ThinkingDisplay   *string `json:"thinking_display"`
			}
			if err := json.Unmarshal(w.Request, &request); err != nil {
				return err
			}
			var e error
			var receipt claude.ConfigurationReceipt
			switch request.Subtype {
			case "initialize":
				continue
			case "set_model":
				receipt, e = s.SetModel(ctx, claude.ModelChange{Model: request.Model})
			case "set_max_thinking_tokens":
				receipt, e = s.SetThinking(ctx, claude.ThinkingChange{MaxThinkingTokens: request.MaxThinkingTokens, ThinkingDisplay: request.ThinkingDisplay})
			case "set_permission_mode":
				receipt, e = s.SetPermissionMode(ctx, request.Mode)
			case "interrupt":
				e = s.Interrupt(ctx, claude.InterruptOptions{})
			default:
				return fmt.Errorf("unsupported control %s", request.Subtype)
			}
			// Expected native rejection is a result too; it must remain a rejection.
			var expected control
			for _, r := range frames {
				if r.Direction == "s2c" {
					var candidate control
					_ = json.Unmarshal(r.Wire, &candidate)
					if candidate.Type == "control_response" && candidate.Response.RequestID == w.RequestID {
						expected = candidate
						break
					}
				}
			}
			if expected.Response.Subtype == "error" {
				var native *claude.ControlError
				if e == nil || !errors.As(e, &native) {
					return fmt.Errorf("%s did not reproduce native rejection: %v", request.Subtype, e)
				}
				continue
			}
			if e != nil {
				return e
			}
			if request.Subtype != "interrupt" && (!receipt.Accepted || !sameJSON(receipt.Requested, w.Request)) {
				return errors.New("configuration request/acknowledgement differs")
			}
		case "control_response":
			mu.Lock()
			r := requests[w.Response.RequestID]
			mu.Unlock()
			if r == nil {
				return errors.New("original request handle missing")
			}
			if r.Method() == "mcp_message" {
				continue
			}
			var answer struct {
				Behavior, Message  string
				UpdatedInput       json.RawMessage          `json:"updatedInput"`
				UpdatedPermissions []types.PermissionUpdate `json:"updatedPermissions"`
				Interrupt          *bool
			}
			if err := json.Unmarshal(w.Response.Response, &answer); err != nil {
				return err
			}
			if answer.Behavior == "deny" {
				err = r.Deny(ctx, claude.DenialOptions{Message: answer.Message, Interrupt: answer.Interrupt})
			} else if r.Handle().ToolName() == "AskUserQuestion" {
				var input struct{ Answers map[string]string }
				if err := json.Unmarshal(answer.UpdatedInput, &input); err != nil {
					return err
				}
				err = r.Answer(ctx, claude.QuestionAnswers{Answers: input.Answers})
			} else {
				err = r.Approve(ctx, claude.ApprovalOptions{UpdatedInput: answer.UpdatedInput, UpdatedPermissions: answer.UpdatedPermissions})
			}
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported client frame %s", w.Type)
		}
	}
	if plan.DriverProfile == "continuation" && (submitted < 2 || terminals != submitted) {
		return errors.New("continuation input/result count differs")
	}
	if submitted == 0 || terminals == 0 && plan.DriverProfile != "session-close" {
		return errors.New("flow has no observed input/result")
	}
	if err := s.Close(); err != nil {
		return err
	}
	<-done
	if _, e := s.Submit(ctx, claude.Input{Text: "must not be sent"}); !errors.Is(e, claude.ErrClosed) {
		return errors.New("closed Session accepted input")
	}
	return nil
}
