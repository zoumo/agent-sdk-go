package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// This independent opt-in spends at most five attempts, with no write replay.
// Read-only sandbox and inherited approval policy are never weakened.
type StdioInteractionsInterruptSuite struct {
	suite.Suite
	executable string
}

func TestStdioInteractionsInterrupt(t *testing.T) {
	if os.Getenv("CODEX_E2E_INTERACTIONS") != "1" {
		t.Skip("real-agent E2E scenario skipped: set CODEX_E2E_INTERACTIONS=1 and CODEX_E2E_CLI to opt in; offline pass is not real-agent evidence")
	}
	require.Empty(t, os.Getenv("CI"), "real-agent E2E tests are forbidden in CI")
	suite.Run(t, new(StdioInteractionsInterruptSuite))
}

func (s *StdioInteractionsInterruptSuite) SetupTest() {
	t := s.T()
	if os.Getenv("CODEX_E2E_INTERACTIONS") != "1" {
		t.Skip("real-agent E2E scenario skipped: set CODEX_E2E_INTERACTIONS=1 and CODEX_E2E_CLI to opt in; offline pass is not real-agent evidence")
	}
	s.executable = os.Getenv("CODEX_E2E_CLI")
	require.True(t, filepath.IsAbs(s.executable),
		"absolute CODEX_E2E_CLI path required")

}

func (s *StdioInteractionsInterruptSuite) TestLifecycle() {
	t := s.T()
	executable := s.executable
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	attempts := 0
	defer func() {
		t.Logf("ticket02_attempts=%d global_used=%d global_remaining=%d", attempts, 2+attempts, 7-attempts)
	}()
	cwd := t.TempDir()
	initCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	c, err := codex.NewStdio(initCtx, codex.StdioOptions{Executable: executable, Args: []string{"app-server"}, Dir: cwd, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "sdk-e2e-interactions", Version: "1"}, Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})}})
	stop()
	require.True(t, err == nil,
		"initialize failed: %T; payload withheld", err)

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
		session, e := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: types.ThreadStartParams{Cwd: types.Some(cwd), Sandbox: types.Some(types.SandboxModeReadOnly), Ephemeral: types.Some(true)}})
		require.True(t, e == nil,
			"thread/start failed: %T", e)

		th, ok := session.StartResponse()
		require.True(t, ok, "native startup response missing")
		id := th.Thread.ID
		require.False(t, id == "" || th.Sandbox.ReadOnly == nil,
			"identity/read-only sandbox assertion failed")

		policy := "other"
		for _, v := range []types.AskForApprovalValue{types.AskForApprovalValueNever, types.AskForApprovalValueOnRequest, types.AskForApprovalValueUntrusted} {
			if th.ApprovalPolicy.Value != nil && *th.ApprovalPolicy.Value == v {
				policy = string(v)
			}
		}
		t.Logf("scenario=%s native thread created read-only; inherited approval_policy=%s", sc.name, policy)

		p := codex.Input{Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: sc.prompt})}}}
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
		go func() { v, e := session.Submit(ctx, p); starts <- result{v.Response, e} }()
		receiveCtx, stopReceive := context.WithCancel(ctx)
		records := make(chan codex.Received)
		receiveErrors := make(chan error, 1)
		readerDone := make(chan struct{})
		defer func() { stopReceive(); _ = session.Close(); <-readerDone }()
		go func() {
			defer close(readerDone)
			for {
				record, err := session.Receive(receiveCtx)
				if err != nil {
					receiveErrors <- err
					return
				}
				select {
				case records <- record:
				case <-receiveCtx.Done():
					return
				}
			}
		}()
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
					require.FailNowf(t, "assertion failed", "%s turn/start failed; no replay: %T", sc.name, r.e)
				}
				response = r.v.Turn.ID
				if response == "" || (turn != "" && turn != response) {
					require.FailNow(t, "RPC/event identity mismatch")
				}
				turn = response
				t.Logf("scenario=%s turn/start responded native ID matched", sc.name)
			case record := <-records:
				if record.Request != nil {
					r := record.Request

					if seen[r.ID()] {
						require.FailNow(t, "duplicate request ID")
					}
					seen[r.ID()] = true
					d, e := r.Data()
					if e != nil {
						require.FailNowf(t, "assertion failed", "typed request decode failed: %T", e)
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
						require.FailNowf(t, "assertion failed", "response failed; no replay: %T", e)
					}
					if !errors.Is(r.Handle().Err(), codex.ErrRequestAnswered) {
						require.FailNow(t, "one-shot terminal state mismatch")
					}
					t.Logf("scenario=%s request=%s native_identity_matched=%t single_response_written=true (not server acceptance)", sc.name, r.Method(), matched)
					continue
				}
				if record.Event == nil {
					continue
				}
				ev := *record.Event

				native := ev
				if native.DecodeError != nil {
					require.FailNow(t, "event loss/decode failure")
				}
				switch v := native.Data.(type) {
				case *types.TurnStartedNotification:
					if v.ThreadID == id {
						require.False(t, turn != "" && turn != v.Turn.ID,
							"turn identity mismatch")

						turn = v.Turn.ID
						t.Logf("scenario=%s matched turn/started", sc.name)
					}
				case *types.ItemStartedNotification:
					if v.ThreadID == id && v.TurnID == turn && sc.name == "interrupt" && v.Item.CommandExecution != nil && v.Item.CommandExecution.Command == "sleep 20" && !interruptSent {
						itemID = v.Item.CommandExecution.ID
						interruptSent = true
						e := session.Interrupt(ctx, codex.InterruptOptions{TurnID: turn})
						require.True(t, e == nil,
							"interrupt failed; no replay: %T", e)

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
					require.FailNowf(t, "assertion failed", "%s native error event; payload withheld", sc.name)
				case *types.TurnCompletedNotification:
					if v.ThreadID == id && v.Turn.ID == turn {
						status = string(v.Turn.Status)
						t.Logf("scenario=%s matched turn/completed status=%s", sc.name, status)
					}
				}
			case err := <-receiveErrors:
				require.FailNowf(t, "receive ended", "Session failed: %T", err)
			case <-session.Done():
				require.FailNowf(t, "assertion failed", "subscription ended early: %T", session.Err())
			case <-timer.C:
				if turn != "" && !interruptSent {
					_ = session.Interrupt(ctx, codex.InterruptOptions{TurnID: turn})
				}
				require.FailNow(t, "turn timed out; at most one interrupt; termination unconfirmed")
			case <-ctx.Done():
				require.FailNow(t, "ticket wall clock exhausted")
			}
		}
		timer.Stop()
		stopReceive()
		<-readerDone
		{
			e := session.Close()
			require.False(t, e != nil || session.Err() != nil,
				"subscription close/loss failure")
		}

		t.Logf("scenario=%s target_requests=%d answers=%d resolutions=%d command_effect=%t blue_exact=%t interrupt_ack=%t interrupted_terminal=%t item_stopped=%t", sc.name, targets, answers, resolved, commandEffect, blue, ack, status == "interrupted", itemStopped)
		require.False(t, status == "failed",
			"server turn failed; not untriggered")

	}
	closeStart := time.Now()
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case e := <-closed:
		if e != nil || !errors.Is(c.Err(), codex.ErrClosed) {
			require.FailNow(t, "close failed")
		}
	case <-time.After(5 * time.Second):
		require.FailNow(t, "close exceeded observation budget")
	}
	select {
	case <-c.Done():
	default:
		require.FailNow(t, "Done open")
	}
	t.Logf("Close=nil Done=closed ErrClosed owned child reaped elapsed_ms=%d", time.Since(closeStart).Milliseconds())
}
