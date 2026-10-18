package live_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// This independent opt-in spends at most five attempts, with no write replay.
// Read-only sandbox and inherited approval policy are never weakened.
func TestStdioInteractionsInterrupt(t *testing.T) {
	if os.Getenv("RUN_MAC_INTERACTIONS_LIVE") != "1" {
		t.Skip("live scenario skipped: set RUN_MAC_INTERACTIONS_LIVE=1 and MAC_LIVE_CODEX to opt in; offline pass is not live evidence")
	}
	executable := os.Getenv("MAC_LIVE_CODEX")
	if executable == "" {
		t.Fatal("explicit executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	attempts := 0
	defer func() {
		t.Logf("ticket02_attempts=%d global_used=%d global_remaining=%d", attempts, 2+attempts, 7-attempts)
	}()
	cwd := t.TempDir()
	initCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	c, err := codex.NewStdio(initCtx, codex.StdioOptions{Executable: executable, Args: []string{"app-server"}, Dir: cwd, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-mac-live-ticket02", Version: "1"}, Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})}})
	stop()
	if err != nil {
		t.Fatalf("initialize failed: %T; payload withheld", err)
	}
	defer c.Close()
	var modes struct {
		Data []struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	err = c.Call(ctx, "collaborationMode/list", struct{}{}, &modes)
	plan := false
	if err == nil {
		for _, m := range modes.Data {
			plan = plan || m.Mode == "plan"
		}
	}
	t.Logf("experimentalApi declared; collaborationMode/list responded=%t plan_advertised=%t", err == nil, plan)
	scenarios := []struct{ name, prompt string }{
		{"command", "Run exactly printf SDK_T02_COMMAND once in the current directory with the normal sandbox. Do not read files, access network, or request extra permissions. Then stop."},
		{"file", "Use apply_patch once to create only ticket02-marker.txt in the current directory containing SDK_T02_FILE and a newline. Do not use shell, read other files, access network, or request broader permissions. If sandbox prevents it, stop and report that."},
		{"permissions", "If request_permissions is available, invoke it once requesting only read access to the current directory, already readable in the sandbox. Do not request network, writes, external paths, or expanded access. If unavailable, reply UNAVAILABLE. Do not run commands or read files."},
		{"user-input", "Use request_user_input once to ask one synthetic color question with exactly two options BLUE and RED. After receiving the answer, reply with exactly that option. Do not use other tools or access files or network."},
		{"interrupt", "Run exactly sleep 20 once with the normal sandbox in the current directory. Do not read or write files, access network, or request extra permissions. Wait for completion before replying."},
	}
	for _, sc := range scenarios {
		if sc.name == "user-input" && !plan {
			t.Log("scenario=user-input environment-blocked: plan mode not advertised; no turn dispatched")
			continue
		}
		th, e := c.StartThread(ctx, types.ThreadStartParams{Cwd: types.Some(cwd), Sandbox: types.Some(types.SandboxModeReadOnly), Ephemeral: types.Some(true)})
		if e != nil {
			t.Fatalf("thread/start failed: %T", e)
		}
		id := th.Thread.ID
		if id == "" || th.Sandbox.ReadOnly == nil {
			t.Fatal("identity/read-only sandbox assertion failed")
		}
		policy := "other"
		for _, v := range []types.AskForApprovalValue{types.AskForApprovalValueNever, types.AskForApprovalValueOnRequest, types.AskForApprovalValueUntrusted} {
			if th.ApprovalPolicy.Value != nil && *th.ApprovalPolicy.Value == v {
				policy = string(v)
			}
		}
		t.Logf("scenario=%s native thread created read-only; inherited approval_policy=%s", sc.name, policy)
		s, e := c.Subscribe(ctx, id, types.SubscriptionOptions{})
		if e != nil {
			t.Fatal("subscribe failed")
		}
		p := types.TurnStartParams{ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: sc.prompt})}}}
		if sc.name == "user-input" {
			p.CollaborationMode = types.Some(types.CollaborationMode{Mode: types.ModeKindPlan, Settings: types.Settings{Model: th.Model, DeveloperInstructions: types.Null[string](), ReasoningEffort: types.Null[types.ReasoningEffort]()}})
		}
		type result struct {
			v types.TurnStartResponse
			e error
		}
		starts := make(chan result, 1)
		attempts++
		// Start asynchronously so events and reverse requests are consumed while
		// the RPC is pending, not only after its response.
		go func() { v, e := c.StartTurn(ctx, p); starts <- result{v, e} }()
		timer := time.NewTimer(120 * time.Second)
		turn := ""
		response := ""
		status := ""
		targets, answers, resolved := 0, 0, 0
		ack, interruptSent, commandEffect, blue, itemStopped := false, false, false, false, false
		itemID := ""
		seen := map[codex.ID]bool{}
		for status == "" || response == "" {
			select {
			case r := <-starts:
				if r.e != nil {
					t.Fatalf("%s turn/start failed; no replay: %T", sc.name, r.e)
				}
				response = r.v.Turn.ID
				if response == "" || (turn != "" && turn != response) {
					t.Fatal("RPC/event identity mismatch")
				}
				turn = response
				t.Logf("scenario=%s turn/start responded native ID matched", sc.name)
			case r, ok := <-c.Requests():
				if !ok {
					t.Fatal("requests ended")
				}
				if seen[r.ID()] {
					t.Fatal("duplicate request ID")
				}
				seen[r.ID()] = true
				d, e := r.Data()
				if e != nil {
					t.Fatalf("typed request decode failed: %T", e)
				}
				matched := false
				var answer any
				switch v := d.(type) {
				case *types.CommandExecutionRequestApprovalParams:
					matched = v.ThreadID == id && v.TurnID == turn
					// Approvals are declined: this test never expands read-only rights.
					answer = types.CommandExecutionRequestApprovalResponse{Decision: types.CommandExecutionApprovalDecision{Value: types.Ptr(types.CommandExecutionApprovalDecisionValueDecline)}}
					if matched && sc.name == "command" {
						targets++
					}
				case *types.FileChangeRequestApprovalParams:
					matched = v.ThreadID == id && v.TurnID == turn
					answer = types.FileChangeRequestApprovalResponse{Decision: types.FileChangeApprovalDecisionDecline}
					if matched && sc.name == "file" {
						targets++
					}
				case *types.PermissionsRequestApprovalParams:
					matched = v.ThreadID == id && v.TurnID == turn
					answer = types.PermissionsRequestApprovalResponse{Permissions: types.GrantedPermissionProfile{}, Scope: types.Some(types.PermissionGrantScopeTurn)}
					if matched && sc.name == "permissions" {
						targets++
					}
				case *types.ToolRequestUserInputParams:
					matched = v.ThreadID == id && v.TurnID == turn && sc.name == "user-input" && len(v.Questions) == 1
					if matched {
						q := v.Questions[0]
						opts, _ := q.Options.Get()
						labels := map[string]bool{}
						for _, o := range opts {
							labels[o.Label] = true
						}
						matched = len(opts) == 2 && labels["BLUE"] && labels["RED"] && !q.IsSecret.ValueOr(false)
						if matched {
							answer = types.ToolRequestUserInputResponse{Answers: map[string]types.ToolRequestUserInputAnswer{q.ID: {Answers: []string{"BLUE"}}}}
							targets++
						}
					}
				}
				aCtx, done := context.WithTimeout(ctx, 30*time.Second)
				if matched && answer != nil {
					e = r.Handle().Answer(aCtx, answer)
					answers++
				} else {
					e = r.Handle().Reject(aCtx, &codex.RPCError{Code: -32602, Message: "outside isolated validation scope"})
				}
				done()
				if e != nil {
					t.Fatalf("response failed; no replay: %T", e)
				}
				if !errors.Is(r.Handle().Err(), codex.ErrRequestAnswered) {
					t.Fatal("one-shot terminal state mismatch")
				}
				t.Logf("scenario=%s request=%s native_identity_matched=%t single_response_written=true (not server acceptance)", sc.name, r.Method(), matched)
			case ev, ok := <-s.Events():
				if !ok {
					t.Fatal("event stream closed")
				}
				if ev.Raw == nil {
					continue
				}
				native := ev
				if native.DecodeError != nil {
					t.Fatal("event loss/decode failure")
				}
				switch v := native.Data.(type) {
				case *types.TurnStartedNotification:
					if v.ThreadID == id {
						if turn != "" && turn != v.Turn.ID {
							t.Fatal("turn identity mismatch")
						}
						turn = v.Turn.ID
						t.Logf("scenario=%s matched turn/started", sc.name)
					}
				case *types.ItemStartedNotification:
					if v.ThreadID == id && v.TurnID == turn && sc.name == "interrupt" && v.Item.CommandExecution != nil && v.Item.CommandExecution.Command == "sleep 20" && !interruptSent {
						itemID = v.Item.CommandExecution.ID
						interruptSent = true
						_, e := c.InterruptTurn(ctx, types.TurnInterruptParams{ThreadID: id, TurnID: turn})
						if e != nil {
							t.Fatalf("interrupt failed; no replay: %T", e)
						}
						ack = true
						t.Log("scenario=interrupt active sleep item observed; exact native turn interrupt ack")
					}
				case *types.ItemCompletedNotification:
					if v.ThreadID == id && v.TurnID == turn {
						if cmd := v.Item.CommandExecution; cmd != nil {
							commandEffect = commandEffect || (sc.name == "command" && strings.TrimSpace(cmd.AggregatedOutput.ValueOr("")) == "SDK_T02_COMMAND" && cmd.ExitCode.ValueOr(-1) == 0)
							itemStopped = itemStopped || (itemID != "" && cmd.ID == itemID && cmd.Status != types.CommandExecutionStatusInProgress)
							t.Logf("scenario=%s matched item/completed command status=%s", sc.name, cmd.Status)
						}
						if msg := v.Item.AgentMessage; msg != nil {
							blue = strings.TrimSpace(msg.Text) == "BLUE"
						}
						if v.Item.FileChange != nil {
							t.Logf("scenario=%s matched item/completed fileChange", sc.name)
						}
					}
				case *types.ServerRequestResolvedNotification:
					raw, _ := json.Marshal(v.RequestID)
					rid, e := codex.ParseID(raw)
					if e == nil && v.ThreadID == id && seen[rid] {
						resolved++
						t.Logf("scenario=%s matched serverRequest/resolved exact tagged ID", sc.name)
					}
				case *types.ErrorNotification:
					t.Fatalf("%s native error event; payload withheld", sc.name)
				case *types.TurnCompletedNotification:
					if v.ThreadID == id && v.Turn.ID == turn {
						status = string(v.Turn.Status)
						t.Logf("scenario=%s matched turn/completed status=%s", sc.name, status)
					}
				}
			case <-s.Done():
				t.Fatalf("subscription ended early: %T", s.Err())
			case <-timer.C:
				if turn != "" && !interruptSent {
					_, _ = c.InterruptTurn(ctx, types.TurnInterruptParams{ThreadID: id, TurnID: turn})
				}
				t.Fatal("turn timed out; at most one interrupt; termination unconfirmed")
			case <-ctx.Done():
				t.Fatal("ticket wall clock exhausted")
			}
		}
		timer.Stop()
		if e := s.Close(); e != nil || s.Err() != nil {
			t.Fatal("subscription close/loss failure")
		}
		t.Logf("scenario=%s target_requests=%d answers=%d resolutions=%d command_effect=%t blue_exact=%t interrupt_ack=%t interrupted_terminal=%t item_stopped=%t", sc.name, targets, answers, resolved, commandEffect, blue, ack, status == "interrupted", itemStopped)
		if status == "failed" {
			t.Fatal("server turn failed; not untriggered")
		}
	}
	closeStart := time.Now()
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case e := <-closed:
		if e != nil || !errors.Is(c.Err(), codex.ErrClosed) {
			t.Fatal("close failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close exceeded observation budget")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("Done open")
	}
	t.Logf("Close=nil Done=closed ErrClosed owned child reaped elapsed_ms=%d", time.Since(closeStart).Milliseconds())
}
