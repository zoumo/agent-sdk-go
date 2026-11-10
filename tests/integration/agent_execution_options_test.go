package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

const (
	agentExecutionSessionID  = "44444444-4444-4444-8444-444444444444"
	agentExecutionSuccess    = "agent-execution-success"
	agentExecutionResume     = "agent-execution-resume"
	agentExecutionReject     = "agent-execution-reject"
	agentExecutionUnknown    = "agent-execution-unknown"
	agentExecutionConcurrent = "agent-execution-concurrent"
)

func (s *NativeIntegrationSuite) TestAgentExecutionOptionsRuntimeEvidenceAcrossProviders() {
	t := s.T()
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tracePath := agentExecutionTracePath(t)
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionSuccess,
			agent.ExecutionOptions{
				Model:           " Caller/Model v2 ",
				ReasoningEffort: " Native/High ",
			},
		)
		controller, ok := session.(agent.ExecutionOptionsController)
		require.True(t, ok)

		initial := controller.EffectiveExecutionOptions()
		requireAgentExecutionValue(
			t,
			" Caller/Model v2 ",
			new("observed/model"),
			initial.Model,
		)
		requireAgentExecutionValue(
			t,
			" Native/High ",
			agentExecutionInitialEffort(providerName),
			initial.ReasoningEffort,
		)

		stream, err := session.Events()
		require.NoError(t, err)
		submitAgentExecutionInput(t, ctx, session, stream, "before/update")

		effortReceipt, err := controller.SetExecutionOption(
			ctx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "next/effort",
			},
		)
		require.NoError(t, err)
		assertAgentExecutionAccepted(t, effortReceipt.Acceptance)
		requireAgentExecutionValue(
			t,
			" Caller/Model v2 ",
			new("observed/model"),
			effortReceipt.Report.Model,
		)
		requireAgentExecutionValue(
			t,
			"next/effort",
			nil,
			effortReceipt.Report.ReasoningEffort,
		)
		assert.Equal(t, effortReceipt.Report, controller.EffectiveExecutionOptions())

		submitAgentExecutionInput(t, ctx, session, stream, "after/effort")
		observedBeforeModel := controller.EffectiveExecutionOptions()
		requireAgentExecutionValue(
			t,
			" Caller/Model v2 ",
			new("observed/model"),
			observedBeforeModel.Model,
		)
		// Claude exposes no separate effort observation; Codex confirms the
		// requested effort before the model update can invalidate it.
		var wantObservedEffort *string
		if providerName == "codex" {
			wantObservedEffort = new("next/effort")
		}
		requireAgentExecutionValue(
			t,
			"next/effort",
			wantObservedEffort,
			observedBeforeModel.ReasoningEffort,
		)

		modelReceipt, err := controller.SetExecutionOption(
			ctx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  "next/model",
			},
		)
		require.NoError(t, err)
		assert.Equal(t, agent.ExecutionOptionModel, modelReceipt.Change.Option)
		assert.Equal(t, "next/model", modelReceipt.Change.Value)
		assertAgentExecutionAccepted(t, modelReceipt.Acceptance)
		requireAgentExecutionValue(t, "next/model", nil, modelReceipt.Report.Model)
		requireAgentExecutionValue(t, "next/effort", nil, modelReceipt.Report.ReasoningEffort)
		assert.Equal(t, modelReceipt.Report, controller.EffectiveExecutionOptions())

		submitAgentExecutionInput(t, ctx, session, stream, "after/model")
		assertAgentExecutionRuntimeTrace(t, providerName, tracePath)

		require.NoError(t, session.Close(ctx))
		afterClose := controller.EffectiveExecutionOptions()
		assert.Equal(t, modelReceipt.Report, afterClose)
		closedReceipt, err := controller.SetExecutionOption(
			ctx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionModel,
				Value:  "after/close",
			},
		)
		require.Error(t, err)
		assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
		require.NotNil(t, closedReceipt.Acceptance.Sent)
		assert.False(t, *closedReceipt.Acceptance.Sent)
		assert.Nil(t, closedReceipt.Acceptance.NativeAccepted)
		assert.Equal(t, afterClose, closedReceipt.Report)
	})
}

func (s *NativeIntegrationSuite) TestAgentExecutionOptionsResumeAcrossProviders() {
	t := s.T()
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tracePath := agentExecutionTracePath(t)
		cwd := t.TempDir()
		configDir := ""
		if providerName == "claude" {
			configDir = t.TempDir()
			writeAgentExecutionResumeTranscript(t, configDir, cwd)
		}
		provider := newAgentExecutionProvider(
			t,
			providerName,
			agentExecutionResume,
			configDir,
		)
		t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
		_, err := provider.Start(ctx)
		require.NoError(t, err)
		session, err := provider.Resume(ctx, agent.ResumeOptions{
			SessionID: agentExecutionSessionID,
			OpenOptions: agent.OpenOptions{
				Cwd: cwd,
				Execution: agent.ExecutionOptions{
					Model:           " Resume/Model ",
					ReasoningEffort: " Native/Resume ",
				},
			},
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			closeAgentExecutionSession(t, session, false, false)
		})

		reporter, ok := session.(agent.ExecutionOptionsReporter)
		require.True(t, ok)
		report := reporter.EffectiveExecutionOptions()
		requireAgentExecutionValue(
			t,
			" Resume/Model ",
			new("observed/model"),
			report.Model,
		)
		requireAgentExecutionValue(
			t,
			" Native/Resume ",
			agentExecutionInitialEffort(providerName),
			report.ReasoningEffort,
		)
		assert.Equal(t, agentExecutionSessionID, session.ID())
		assertAgentExecutionResumeTrace(t, providerName, tracePath)
		require.NoError(t, session.Close(ctx))
	})
}

func (s *NativeIntegrationSuite) TestAgentExecutionOptionsNativeRejectionAcrossProviders() {
	t := s.T()
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionReject,
			agent.ExecutionOptions{
				Model:           "initial/model",
				ReasoningEffort: "initial-effort",
			},
		)
		controller, ok := session.(agent.ExecutionOptionsController)
		require.True(t, ok)
		before := controller.EffectiveExecutionOptions()

		assertRejected := func(option agent.ExecutionOption, value string) {
			receipt, err := controller.SetExecutionOption(
				ctx,
				agent.ExecutionOptionChange{Option: option, Value: value},
			)
			require.Error(t, err)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
			require.NotNil(t, receipt.Acceptance.Sent)
			require.NotNil(t, receipt.Acceptance.NativeAccepted)
			assert.True(t, *receipt.Acceptance.Sent)
			assert.False(t, *receipt.Acceptance.NativeAccepted)
			assert.Equal(t, before, receipt.Report)
			assert.Equal(t, before, controller.EffectiveExecutionOptions())
		}
		assertRejected(agent.ExecutionOptionReasoningEffort, "rejected/effort")
		assertRejected(agent.ExecutionOptionModel, "rejected/model")
		require.NoError(t, session.Close(ctx))
	})
}

func (s *NativeIntegrationSuite) TestAgentExecutionOptionsUnknownOutcomeAcrossProviders() {
	t := s.T()
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		tests := []struct {
			name               string
			option             agent.ExecutionOption
			value              string
			wantModelEffective *string
		}{
			{
				name:               "model",
				option:             agent.ExecutionOptionModel,
				value:              "uncertain/model",
				wantModelEffective: nil,
			},
			{
				name:               "effort",
				option:             agent.ExecutionOptionReasoningEffort,
				value:              "uncertain/effort",
				wantModelEffective: new("observed/model"),
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				session := openAgentExecutionSession(
					t,
					ctx,
					providerName,
					agentExecutionUnknown,
					agent.ExecutionOptions{
						Model:           "initial/model",
						ReasoningEffort: "initial-effort",
					},
				)
				controller, ok := session.(agent.ExecutionOptionsController)
				require.True(t, ok)

				receipt, err := controller.SetExecutionOption(
					ctx,
					agent.ExecutionOptionChange{
						Option: test.option,
						Value:  test.value,
					},
				)
				require.Error(t, err)
				assert.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.True(t, *receipt.Acceptance.Sent)
				assert.Nil(t, receipt.Acceptance.NativeAccepted)
				requireAgentExecutionValue(
					t,
					"initial/model",
					test.wantModelEffective,
					receipt.Report.Model,
				)
				requireAgentExecutionValue(
					t,
					"initial-effort",
					nil,
					receipt.Report.ReasoningEffort,
				)
				assert.Equal(t, receipt.Report, controller.EffectiveExecutionOptions())
				closeAgentExecutionSession(
					t,
					session,
					true,
					providerName == "claude",
				)
			})
		}
	})
}

func (s *NativeIntegrationSuite) TestAgentExecutionOptionsSerializeConcurrentUpdatesAcrossProviders() {
	t := s.T()
	forEachAgentExecutionProvider(t, func(t *testing.T, providerName string, ctx context.Context) {
		signalPath := t.TempDir() + "/request-seen"
		releasePath := t.TempDir() + "/release-response"
		t.Setenv("AGENT_EXECUTION_SIGNAL", signalPath)
		t.Setenv("AGENT_EXECUTION_RELEASE", releasePath)
		session := openAgentExecutionSession(
			t,
			ctx,
			providerName,
			agentExecutionConcurrent,
			agent.ExecutionOptions{Model: "initial/model"},
		)
		t.Cleanup(func() {
			if err := os.WriteFile(releasePath, []byte("release\n"), 0o600); err != nil {
				t.Errorf("release agent execution peer: %v", err)
			}
		})
		controller, ok := session.(agent.ExecutionOptionsController)
		require.True(t, ok)

		type updateResult struct {
			name    string
			receipt agent.ExecutionOptionsReceipt
			err     error
		}
		results := make(chan updateResult, 2)
		go func() {
			receipt, err := controller.SetExecutionOption(
				ctx,
				agent.ExecutionOptionChange{
					Option: agent.ExecutionOptionModel,
					Value:  "slow/model",
				},
			)
			results <- updateResult{name: "slow", receipt: receipt, err: err}
		}()
		require.Eventually(t, func() bool {
			_, err := os.Stat(signalPath)
			return err == nil
		}, 5*time.Second, 5*time.Millisecond, "first update did not reach the peer")

		fastCtx, cancelFast := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancelFast()
		releaseDone := make(chan error, 1)
		go func() {
			<-fastCtx.Done()
			releaseDone <- os.WriteFile(releasePath, []byte("release\n"), 0o600)
		}()
		// The peer signal above proves the first update is in flight. The
		// second update cannot reach the native write path while the first
		// response is held; Sent=false distinguishes serialization from a
		// parallel dispatch that only timed out waiting for an ack.
		fastReceipt, fastErr := controller.SetExecutionOption(
			fastCtx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "fast-effort",
			},
		)
		require.NoError(t, <-releaseDone)
		slow := <-results
		require.NoError(t, slow.err)
		assertAgentExecutionAccepted(t, slow.receipt.Acceptance)
		requireAgentExecutionValue(t, "slow/model", nil, slow.receipt.Report.Model)
		require.Error(t, fastErr)
		require.NotNil(t, fastReceipt.Acceptance.Sent)
		assert.False(t, *fastReceipt.Acceptance.Sent)

		nextReceipt, err := controller.SetExecutionOption(
			ctx,
			agent.ExecutionOptionChange{
				Option: agent.ExecutionOptionReasoningEffort,
				Value:  "after/serialization",
			},
		)
		require.NoError(t, err)
		assertAgentExecutionAccepted(t, nextReceipt.Acceptance)
		requireAgentExecutionValue(t, "slow/model", nil, nextReceipt.Report.Model)
		requireAgentExecutionValue(
			t,
			"after/serialization",
			nil,
			nextReceipt.Report.ReasoningEffort,
		)
	})
}

func forEachAgentExecutionProvider(
	t *testing.T,
	run func(*testing.T, string, context.Context),
) {
	t.Helper()
	for _, providerName := range []string{"codex", "claude"} {
		t.Run(providerName, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			run(t, providerName, ctx)
		})
	}
}

func agentExecutionPeer(provider, scenario string) (err error) {
	if provider == "claude" && len(os.Args) == 2 && os.Args[1] == "--version" {
		_, err := fmt.Fprintln(os.Stdout, "2.1.258 (Claude Code)")
		return err
	}
	if scenario != agentExecutionSuccess &&
		scenario != agentExecutionResume &&
		scenario != agentExecutionReject &&
		scenario != agentExecutionUnknown &&
		scenario != agentExecutionConcurrent {
		return fmt.Errorf("unsupported agent execution peer scenario %s/%s", provider, scenario)
	}
	sessionID := agentExecutionSessionID
	resumeSeen := false
	for _, arg := range os.Args[1:] {
		if value, ok := strings.CutPrefix(arg, "--resume="); ok {
			sessionID = value
			resumeSeen = true
		}
	}
	if provider == "claude" && scenario == agentExecutionResume && !resumeSeen {
		return errors.New("agent execution resume scenario requires --resume")
	}
	if provider == "claude" && scenario != agentExecutionResume && resumeSeen {
		return errors.New("non-resume agent execution scenario received --resume")
	}
	tracer, err := openAgentExecutionTracer()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tracer.Close()) }()
	if err := tracer.Record("argv", os.Args[1:]); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	heldConcurrentUpdate := false
	codexTurnCount := 0
	pendingEffortObservation := ""
	for scanner.Scan() {
		raw := append(json.RawMessage(nil), scanner.Bytes()...)
		var frame struct {
			ID        json.RawMessage `json:"id"`
			Method    string          `json:"method"`
			Type      string          `json:"type"`
			RequestID string          `json:"request_id"`
			Params    json.RawMessage `json:"params"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			return err
		}
		if err := tracer.Record("frame", raw); err != nil {
			return err
		}
		switch provider {
		case "codex":
			switch frame.Method {
			case "initialize":
				if err := encoder.Encode(map[string]any{
					"id": frame.ID,
					"result": map[string]any{
						"userAgent":      "synthetic",
						"codexHome":      "/synthetic",
						"platformFamily": "unix",
						"platformOs":     "linux",
					},
				}); err != nil {
					return err
				}
			case "initialized":
			case "thread/start":
				if scenario == agentExecutionResume {
					return errors.New("resume scenario used thread/start")
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": agentExecutionCodexResult(sessionID, cwd),
				}); err != nil {
					return err
				}
			case "thread/resume":
				if scenario != agentExecutionResume {
					return errors.New("non-resume scenario used thread/resume")
				}
				var params struct {
					ThreadID string `json:"threadId"`
				}
				if err := json.Unmarshal(frame.Params, &params); err != nil {
					return err
				}
				if params.ThreadID != sessionID {
					return fmt.Errorf(
						"resume thread id %q, want %q",
						params.ThreadID,
						sessionID,
					)
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": agentExecutionCodexResult(sessionID, cwd),
				}); err != nil {
					return err
				}
			case "thread/settings/update":
				if scenario == agentExecutionConcurrent && !heldConcurrentUpdate {
					heldConcurrentUpdate = true
					if err := holdAgentExecutionUpdate(); err != nil {
						return err
					}
				}
				if scenario == agentExecutionUnknown {
					return nil
				}
				if scenario == agentExecutionReject {
					if err := encoder.Encode(map[string]any{
						"id": frame.ID,
						"error": map[string]any{
							"code":    -32602,
							"message": "unsupported model",
						},
					}); err != nil {
						return err
					}
					continue
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": map[string]any{},
				}); err != nil {
					return err
				}
				if scenario == agentExecutionSuccess {
					var params struct {
						Effort *string `json:"effort"`
						Model  *string `json:"model"`
					}
					if err := json.Unmarshal(frame.Params, &params); err != nil {
						return err
					}
					if params.Effort != nil && params.Model == nil {
						pendingEffortObservation = *params.Effort
					}
				}
			case "thread/unsubscribe":
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": map[string]any{"status": "unsubscribed"},
				}); err != nil {
					return err
				}
			case "turn/start":
				codexTurnCount++
				turnID := fmt.Sprintf("turn-after-update-%d", codexTurnCount)
				if pendingEffortObservation != "" {
					if err := encoder.Encode(map[string]any{
						"method": "thread/settings/updated",
						"params": map[string]any{
							"threadId": sessionID,
							"threadSettings": map[string]any{
								"cwd":               cwd,
								"approvalPolicy":    "never",
								"approvalsReviewer": "user",
								"sandboxPolicy":     map[string]any{"type": "readOnly"},
								"model":             "observed/model",
								"modelProvider":     "synthetic",
								"collaborationMode": map[string]any{
									"mode":     "default",
									"settings": map[string]any{"model": "observed/model"},
								},
								"effort": pendingEffortObservation,
							},
						},
					}); err != nil {
						return err
					}
					pendingEffortObservation = ""
				}
				if err := encoder.Encode(map[string]any{
					"id": frame.ID,
					"result": map[string]any{
						"turn": map[string]any{
							"id":     turnID,
							"status": "inProgress",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"method": "turn/started",
					"params": map[string]any{
						"threadId": sessionID,
						"turn": map[string]any{
							"id":     turnID,
							"status": "inProgress",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"method": "turn/completed",
					"params": map[string]any{
						"threadId": sessionID,
						"turn": map[string]any{
							"id":     turnID,
							"status": "completed",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unexpected agent execution Codex request: %s", raw)
			}
		case "claude":
			if frame.Type == "user" {
				at := time.Now().UTC().Format(time.RFC3339Nano)
				if err := encoder.Encode(map[string]any{
					"type":       "system",
					"subtype":    "session_state_changed",
					"session_id": sessionID,
					"state":      "running",
					"timestamp":  at,
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"type":            "result",
					"subtype":         "success",
					"session_id":      sessionID,
					"is_error":        false,
					"result":          "synthetic execution completed",
					"num_turns":       1,
					"duration_ms":     1,
					"duration_api_ms": 1,
					"total_cost_usd":  0,
					"usage":           map[string]any{},
					"timestamp":       at,
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"type":       "system",
					"subtype":    "session_state_changed",
					"session_id": sessionID,
					"state":      "idle",
					"timestamp":  at,
				}); err != nil {
					return err
				}
				continue
			}
			if frame.Type != "control_request" {
				return fmt.Errorf("unexpected agent execution Claude frame: %s", raw)
			}
			response := any(map[string]any{})
			switch frame.Request.Subtype {
			case "get_settings":
				response = map[string]any{"applied": map[string]any{"ultracode": false}}
			case "initialize", "set_model", "apply_flag_settings":
			default:
				return fmt.Errorf(
					"unexpected agent execution Claude control request: %s",
					raw,
				)
			}
			if scenario == agentExecutionReject &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings") {
				if err := encoder.Encode(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "error",
						"request_id": frame.RequestID,
						"error":      "unsupported model",
					},
				}); err != nil {
					return err
				}
				continue
			}
			if scenario == agentExecutionUnknown &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings") {
				return nil
			}
			if scenario == agentExecutionConcurrent &&
				!heldConcurrentUpdate &&
				frame.Request.Subtype == "set_model" {
				heldConcurrentUpdate = true
				if err := holdAgentExecutionUpdate(); err != nil {
					return err
				}
			}
			if err := encoder.Encode(map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype":    "success",
					"request_id": frame.RequestID,
					"response":   response,
				},
			}); err != nil {
				return err
			}
			if frame.Request.Subtype == "initialize" {
				if err := encoder.Encode(map[string]any{
					"type":                "system",
					"subtype":             "init",
					"session_id":          sessionID,
					"cwd":                 cwd,
					"claude_code_version": "2.1.258",
					"model":               "observed/model",
				}); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown agent execution provider %q", provider)
		}
	}
	return scanner.Err()
}

type agentExecutionTracer struct {
	file    *os.File
	encoder *json.Encoder
}

func openAgentExecutionTracer() (*agentExecutionTracer, error) {
	path := os.Getenv("AGENT_EXECUTION_TRACE")
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &agentExecutionTracer{file: file, encoder: json.NewEncoder(file)}, nil
}

func (t *agentExecutionTracer) Record(kind string, body any) error {
	if t == nil {
		return nil
	}
	return t.encoder.Encode(map[string]any{"kind": kind, "body": body})
}

func (t *agentExecutionTracer) Close() error {
	if t == nil {
		return nil
	}
	return t.file.Close()
}

func holdAgentExecutionUpdate() error {
	signalPath := os.Getenv("AGENT_EXECUTION_SIGNAL")
	releasePath := os.Getenv("AGENT_EXECUTION_RELEASE")
	if signalPath == "" || releasePath == "" {
		return errors.New("agent execution update gate is not configured")
	}
	if err := os.WriteFile(signalPath, []byte("request\n"), 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for !agentExecutionFileExists(releasePath) {
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for the agent execution update release")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func agentExecutionFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func agentExecutionCodexResult(sessionID, cwd string) map[string]any {
	return map[string]any{
		"thread": map[string]any{
			"id":            sessionID,
			"sessionId":     sessionID,
			"cliVersion":    "0.162.1",
			"createdAt":     1,
			"updatedAt":     2,
			"cwd":           cwd,
			"ephemeral":     false,
			"modelProvider": "synthetic",
			"preview":       "",
			"projectId":     nil,
			"source":        "appServer",
			"status":        map[string]any{"type": "idle"},
			"turns":         []any{},
		},
		"approvalPolicy":    "never",
		"approvalsReviewer": "user",
		"cwd":               cwd,
		"model":             "observed/model",
		"modelProvider":     "synthetic",
		"reasoningEffort":   "observed-effort",
		"sandbox":           map[string]any{"type": "readOnly"},
	}
}

func openAgentExecutionSession(
	t *testing.T,
	ctx context.Context,
	providerName string,
	scenario string,
	execution agent.ExecutionOptions,
) agent.Session {
	t.Helper()
	provider := newAgentExecutionProvider(t, providerName, scenario, "")
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:       t.TempDir(),
		Execution: execution,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		closeAgentExecutionSession(
			t,
			session,
			scenario == agentExecutionUnknown,
			providerName == "claude" && scenario == agentExecutionUnknown,
		)
	})
	return session
}

func newAgentExecutionProvider(
	t *testing.T,
	providerName string,
	scenario string,
	configDir string,
) agent.Provider {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	env := append(
		os.Environ(),
		"SDK_INTEGRATION_PEER="+providerName,
		"SDK_INTEGRATION="+scenario,
		"GORACE=atexit_sleep_ms=0",
	)
	switch providerName {
	case "codex":
		return agentcodex.New(agentcodex.Config{Stdio: codex.StdioOptions{
			Executable: executable,
			Env:        env,
			Dir:        t.TempDir(),
			Initialize: codextypes.InitializeParams{
				ClientInfo: codextypes.ClientInfo{
					Name:    "agent-execution-integration",
					Version: "synthetic",
				},
			},
		}})
	case "claude":
		return agentclaude.New(agentclaude.Config{
			CLIPath:           executable,
			Env:               env,
			ConfigDir:         configDir,
			InitializeTimeout: 10 * time.Second,
			ShutdownTimeout:   5 * time.Second,
		})
	default:
		require.FailNow(t, "unknown provider", providerName)
		return nil
	}
}

func writeAgentExecutionResumeTranscript(t *testing.T, configDir, cwd string) {
	t.Helper()
	directory := filepath.Join(configDir, "projects", "-synthetic")
	require.NoError(t, os.MkdirAll(directory, 0o700))
	source, err := json.Marshal(map[string]any{
		"type":      "user",
		"sessionId": agentExecutionSessionID,
		"cwd":       cwd,
		"message": map[string]any{
			"role":    "user",
			"content": "saved",
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(directory, agentExecutionSessionID+".jsonl"),
		append(source, '\n'),
		0o600,
	))
}
