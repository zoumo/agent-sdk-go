package agentpi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

func TestPermissionPresetsBeforeLaunch(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, preset := range []agent.PermissionPreset{agent.PermissionPresetManualReview, agent.PermissionPresetAutoReview} {
		t.Run(string(preset), func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "launched")
			dir, cwd := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "old.jsonl"), fmt.Appendf(nil, `{"type":"session","id":"old","cwd":%q}`, cwd), 0o600))
			core := agentpi.New(agentpi.Config{CLIPath: exe, Args: []string{"-test.run=^TestPiRPCPeer$", "--"}, Env: []string{"SDK_PI_PEER=1", "SDK_PI_LAUNCH_MARKER=" + marker}, SessionDir: dir})
			ctx := t.Context()
			_, err := core.Start(ctx)
			require.NoError(t, err)
			options := agent.OpenOptions{Cwd: cwd, PermissionPreset: preset}
			_, err = core.Open(ctx, options)
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			_, err = core.Resume(ctx, agent.ResumeOptions{SessionID: "old", OpenOptions: options})
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			_, err = core.OpenWithOptions(ctx, options, agentpi.SessionOptions{ToolApproval: true})
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			assert.NoFileExists(t, marker)
			require.NoError(t, core.Stop(ctx))
		})
	}
}

func TestExactResumeAndCloseDrain(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	cwd := t.TempDir()
	file := filepath.Join(dir, "persisted.jsonl")
	require.NoError(t, os.WriteFile(file, fmt.Appendf(nil, `{"type":"session","id":"01-test","cwd":%q}`, cwd), 0o600))
	core := agentpi.New(agentpi.Config{CLIPath: exe, Args: []string{"-test.run=^TestPiRPCPeer$", "--"}, Env: []string{"SDK_PI_PEER=1", "SDK_PI_EXPECT_SESSION=" + file, "PI_CODING_AGENT_SESSION_DIR=" + dir}})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	for _, invalid := range []agent.ResumeOptions{
		{SessionID: "01", OpenOptions: agent.OpenOptions{Cwd: cwd}},
		{SessionID: "01-test", OpenOptions: agent.OpenOptions{Cwd: t.TempDir()}},
	} {
		_, err = core.Resume(ctx, invalid)
		require.Error(t, err)
	}
	copyPath := filepath.Join(dir, "duplicate.jsonl")
	require.NoError(t, os.WriteFile(copyPath, fmt.Appendf(nil, `{"type":"session","id":"01-test","cwd":%q}`, cwd), 0o600))
	opts := agent.ResumeOptions{SessionID: "01-test", OpenOptions: agent.OpenOptions{Cwd: cwd}}
	_, err = core.Resume(ctx, opts)
	require.Error(t, err)
	require.NoError(t, os.Remove(copyPath))
	s, err := core.Resume(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, opts.SessionID, s.ID())
	t.Cleanup(func() { _ = s.Close(context.Background()); _ = core.Stop(context.Background()) })
	_, err = core.Resume(ctx, opts)
	require.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
	stream, err := s.Events()
	require.NoError(t, err)
	in := agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}}}
	_, err = s.StartOrSteer(ctx, in)
	require.NoError(t, err)
	require.NoError(t, s.Close(ctx))
	for _, want := range []agent.EventType{agent.EventSessionStateUpdate, agent.EventSessionUnsupportedNativeEvent, agent.EventAgentMessageChunk, agent.EventAgentMessage, agent.EventAgentToolCall, agent.EventAgentToolResult, agent.EventSessionUnsupportedNativeEvent} {
		ev, err := stream.Next(ctx)
		require.NoError(t, err)
		assert.Equal(t, want, ev.Type())
	}
	_, err = stream.Next(ctx)
	require.ErrorIs(t, err, io.EOF)
	opts.PermissionPreset = agent.PermissionPresetFullAccess
	resumed, err := core.Resume(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, s.ID(), resumed.ID())
	require.NoError(t, resumed.Close(ctx))
	require.NoError(t, core.Stop(ctx))
}

func TestPiSessionLifecycle(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, preset := range []agent.PermissionPreset{"", agent.PermissionPresetFullAccess} {
		t.Run("preset-"+string(preset), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			core := agentpi.New(agentpi.Config{CLIPath: exe, Args: []string{"-test.run=^TestPiRPCPeer$", "--"}, Env: []string{"SDK_PI_PEER=1", "SDK_PI_CHECK_OPTIONS=1"}, Provider: "magpie"})
			caps, err := core.Start(ctx)
			require.NoError(t, err)
			session, err := core.Open(ctx, agent.OpenOptions{
				Cwd: t.TempDir(), PermissionPreset: preset,
				Config:                 agent.SessionConfig{Model: "auto/deepseek-v4.1-flash-1017", Effort: "medium"},
				Features:               agent.Features{RestrictLocalTools: true},
				AdditionalSystemPrompt: "Use the supplied context.",
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close(context.Background()); _ = core.Stop(context.Background()) })
			assert.Equal(t, "01-test", session.ID())
			assert.True(t, session.Capabilities().Supports(agent.CapabilityTextInput))
			assert.True(t, caps.Supports(agent.CapabilityImageInput))
			assert.True(t, caps.Supports(agent.CapabilityProcessEnv))
			assert.True(t, session.Capabilities().Supports(agent.CapabilityProcessEnv))
			assert.False(t, session.Capabilities().Supports(agent.CapabilityShellEnv))
			assert.False(t, session.Capabilities().Supports(agent.CapabilityPermission))
			assert.False(t, session.EffectiveFeatures().AgentQuestions)
			assert.True(t, session.EffectiveFeatures().RestrictLocalTools)
			_, ok := session.(agent.Injector)
			assert.False(t, ok)
			require.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(core.Stop(ctx)))
			stream, err := session.Events()
			require.NoError(t, err)
			_, err = session.Events()
			require.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
			idle, err := session.Interrupt(ctx, agent.InterruptOptions{})
			require.NoError(t, err)
			assert.Equal(t, agent.InterruptNoActiveWork, idle.Result)

			cfg := session.(agent.ConfigController)
			changed, err := cfg.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigEffort, Value: "low"})
			require.NoError(t, err)
			require.NotNil(t, changed.Acceptance.NativeAccepted)
			assert.True(t, *changed.Acceptance.NativeAccepted)
			clamped, err := cfg.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigEffort, Value: "max"})
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			require.NotNil(t, clamped.Acceptance.NativeAccepted)
			assert.True(t, *clamped.Acceptance.NativeAccepted)
			model, err := cfg.SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigModel, Value: "auto/another-model"})
			require.NoError(t, err)
			require.NotNil(t, model.Acceptance.NativeAccepted)
			assert.True(t, *model.Acceptance.NativeAccepted)
			input := agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}}}
			receipt, err := session.StartOrSteer(ctx, input)
			require.NoError(t, err)
			assert.Equal(t, agent.InputAppliedNewWork, receipt.Result)
			require.NotEmpty(t, receipt.Submission)
			assert.True(t, *receipt.Acceptance.NativeAccepted)
			ev, err := stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventSessionStateUpdate, ev.Type())
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventSessionUnsupportedNativeEvent, ev.Type()) // message_start
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventAgentMessageChunk, ev.Type())
			chunkID := ev.ItemID
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventAgentMessage, ev.Type())
			assert.Equal(t, chunkID, ev.ItemID)
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventAgentToolCall, ev.Type())
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			assert.Equal(t, agent.EventAgentToolResult, ev.Type())
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			raw := ev.Payload.(agent.UnsupportedNativeEventPayload)
			assert.Equal(t, "pi", raw.AgentType)
			assert.JSONEq(t, `{"type":"future_event","future":{"priority":7,"values":[1,true,null]}}`, string(raw.Raw))
			image := agent.ContentBlock{Image: &agent.ContentBlockImage{Data: "aGk=", MIMEType: "image/png"}}
			joined, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{input.Content[0], image}})
			require.NoError(t, err)
			assert.Equal(t, agent.InputAppliedBestEffort, joined.Result)
			handled, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "/fixture"}}}})
			require.NoError(t, err)
			assert.Equal(t, agent.InputAppliedBestEffort, handled.Result)
			stopped, err := session.Interrupt(ctx, agent.InterruptOptions{})
			require.NoError(t, err)
			assert.Equal(t, agent.InterruptAcknowledged, stopped.Result)
			assert.Equal(t, receipt.Submission, stopped.Target)
			ev, err = stream.Next(ctx)
			require.NoError(t, err)
			state := ev.Payload.(agent.StateUpdatePayload)
			require.Equal(t, agent.StateIdle, *state.State)
			assert.Equal(t, "aborted", state.StopReason)
			after, err := session.Interrupt(ctx, agent.InterruptOptions{})
			require.NoError(t, err)
			assert.Equal(t, agent.InterruptNoActiveWork, after.Result)
			continued, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "continue"}}}})
			require.NoError(t, err)
			assert.Equal(t, agent.InputAppliedNewWork, continued.Result)
			assert.NotEqual(t, receipt.Submission, continued.Submission)
			for _, eventType := range []agent.EventType{
				agent.EventSessionStateUpdate, agent.EventSessionUnsupportedNativeEvent,
				agent.EventAgentMessage, agent.EventSessionStateUpdate,
			} {
				ev, err = stream.Next(ctx)
				require.NoError(t, err)
				require.Equal(t, eventType, ev.Type())
			}
			state = ev.Payload.(agent.StateUpdatePayload)
			require.Equal(t, agent.StateIdle, *state.State)
			assert.Equal(t, "stop", state.StopReason)
			_, err = session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "fail"}}}})
			require.NoError(t, err)
			for _, eventType := range []agent.EventType{
				agent.EventSessionStateUpdate, agent.EventSessionUnsupportedNativeEvent,
				agent.EventSessionUnsupportedNativeEvent, agent.EventSessionError,
				agent.EventSessionUnsupportedNativeEvent, agent.EventSessionStateUpdate,
			} {
				ev, err = stream.Next(ctx)
				require.NoError(t, err)
				require.Equal(t, eventType, ev.Type())
			}
			state = ev.Payload.(agent.StateUpdatePayload)
			require.Equal(t, agent.StateIdle, *state.State)
			assert.Equal(t, "error", state.StopReason)
			require.NotNil(t, state.Error)
			assert.Equal(t, "connection lost", state.Error.Message)
			require.NoError(t, session.Close(ctx))
			require.NoError(t, core.Stop(ctx))
		})
	}
}

func TestPiSettledBeforeStartedReply(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	core := agentpi.New(agentpi.Config{
		CLIPath: exe, Args: []string{"-test.run=^TestPiRPCPeer$", "--"}, Env: []string{"SDK_PI_PEER=1"},
		SessionDir: t.TempDir(),
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	session, err := core.Open(ctx, agent.OpenOptions{Cwd: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	stream, err := session.Events()
	require.NoError(t, err)

	type result struct {
		receipt agent.InputReceipt
		err     error
	}
	input := agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "settle before reply"}}}}
	replies := make(chan result, 1)
	go func() {
		receipt, err := session.StartOrSteer(ctx, input)
		replies <- result{receipt: receipt, err: err}
	}()
	for _, want := range []agent.SessionState{agent.StateRunning, agent.StateIdle} {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		payload, ok := event.Payload.(agent.StateUpdatePayload)
		require.True(t, ok)
		require.Equal(t, &want, payload.State)
	}
	busy, err := session.StartOrSteer(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, agent.InputNotApplied, busy.Result)
	assert.Equal(t, new(false), busy.Acceptance.Sent)

	// The peer waits for this command before sending the held prompt reply.
	_, err = session.(agent.ConfigController).SetConfig(ctx, agent.ConfigChange{ID: agent.ConfigEffort, Value: "low"})
	require.NoError(t, err)
	got := <-replies
	require.NoError(t, got.err)
	assert.Equal(t, agent.InputAppliedNewWork, got.receipt.Result)
	assert.Equal(t, new(true), got.receipt.Acceptance.NativeAccepted)
	assert.NotEmpty(t, got.receipt.Submission)
	stopped, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptNoActiveWork, stopped.Result)
	assert.Empty(t, stopped.Target)

	next, err := session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}}})
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, next.Result)
	assert.NotEqual(t, got.receipt.Submission, next.Submission)
	stopped, err = session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	assert.Equal(t, agent.InterruptAcknowledged, stopped.Result)
	assert.Equal(t, next.Submission, stopped.Target)
}

func TestPiMessageEndMetadataIsolation(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	core := agentpi.New(agentpi.Config{
		CLIPath: exe, Args: []string{"-test.run=^TestPiRPCPeer$", "--"}, Env: []string{"SDK_PI_PEER=1"},
		SessionDir: t.TempDir(),
	})
	_, err = core.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	session, err := core.Open(ctx, agent.OpenOptions{Cwd: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	stream, err := session.Events()
	require.NoError(t, err)
	_, err = session.StartOrSteer(ctx, agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "think and answer"}}}})
	require.NoError(t, err)
	for _, want := range []agent.EventType{agent.EventSessionStateUpdate, agent.EventSessionUnsupportedNativeEvent} {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		require.Equal(t, want, event.Type())
	}
	thinking, err := stream.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, agent.EventAgentThinking, thinking.Type())
	message, err := stream.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, agent.EventAgentMessage, message.Type())
	require.NoError(t, session.Close(ctx))

	thinkingPi, ok := thinking.Meta["pi"].(map[string]any)
	require.True(t, ok)
	messagePi, ok := message.Meta["pi"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "message_end", thinkingPi["nativeType"])
	require.Equal(t, "message_end", messagePi["nativeType"])

	thinking.Meta["consumer"] = true
	thinkingPi["nativeType"] = "modified"
	assert.NotContains(t, message.Meta, "consumer")
	assert.Equal(t, "message_end", messagePi["nativeType"])
}

// TestPiRPCPeer runs only as a purpose-built test subprocess.
func TestPiRPCPeer(t *testing.T) {
	if os.Getenv("SDK_PI_PEER") != "1" {
		return
	}
	if marker := os.Getenv("SDK_PI_LAUNCH_MARKER"); marker != "" {
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			os.Exit(26)
		}
	}
	if !slices.Contains(os.Args, "--no-extensions") {
		os.Exit(20)
	}
	if expected := os.Getenv("SDK_PI_EXPECT_SESSION"); expected != "" {
		index := slices.Index(os.Args, "--session")
		if index < 0 || index+1 >= len(os.Args) || os.Args[index+1] != expected {
			os.Exit(33)
		}
	}
	thinking := "off"
	if os.Getenv("SDK_PI_CHECK_OPTIONS") == "1" {
		for flag, want := range map[string]string{
			"--provider": "magpie",
			"--model":    "auto/deepseek-v4.1-flash-1017",
			"--thinking": "medium",
		} {
			index := slices.Index(os.Args, flag)
			if index < 0 || index+1 >= len(os.Args) || os.Args[index+1] != want {
				os.Exit(27)
			}
		}
		index := slices.Index(os.Args, "--append-system-prompt")
		if index < 0 || index+1 >= len(os.Args) || !slices.Contains(os.Args, "--no-tools") {
			os.Exit(28)
		}
		data, err := os.ReadFile(os.Args[index+1])
		if err != nil || string(data) != "Use the supplied context." {
			os.Exit(29)
		}
		thinking = "medium"
	}
	emit := json.NewEncoder(os.Stdout)
	scan := bufio.NewScanner(os.Stdin)
	active := false
	var delayedPrompt map[string]any
	for scan.Scan() {
		var req struct {
			ID                string `json:"id"`
			Type              string `json:"type"`
			StreamingBehavior string `json:"streamingBehavior"`
			Message           string `json:"message"`
			Level             string `json:"level"`
			Provider          string `json:"provider"`
			Model             string `json:"modelId"`
			Images            []struct {
				Type     string `json:"type"`
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			} `json:"images"`
		}
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			os.Exit(21)
		}
		resp := map[string]any{"id": req.ID, "type": "response", "success": true, "command": req.Type}
		var events []string
		switch req.Type {
		case "get_state":
			resp["data"] = map[string]any{"sessionId": "01-test", "isStreaming": active, "thinkingLevel": thinking}
		case "set_thinking_level":
			if delayedPrompt != nil {
				if err := emit.Encode(delayedPrompt); err != nil {
					os.Exit(25)
				}
				delayedPrompt = nil
			}
			thinking = req.Level
			if thinking == "max" {
				thinking = "high"
			}
		case "set_model":
			if req.Provider != "magpie" || req.Model != "auto/another-model" {
				os.Exit(30)
			}
		case "prompt":
			if req.StreamingBehavior != "steer" || delayedPrompt != nil {
				os.Exit(22)
			}
			// Check that local tracing adds no fields to the Pi prompt.
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(scan.Bytes(), &fields); err != nil {
				os.Exit(21)
			}
			for key := range fields {
				switch key {
				case "id", "type", "message", "images", "streamingBehavior":
				default:
					os.Exit(34)
				}
			}
			if req.Message == "/fixture" {
				resp["data"] = map[string]string{"disposition": "handled"}
				break
			}
			if req.Message == "settle before reply" && !active {
				resp["data"] = map[string]string{"disposition": "started"}
				delayedPrompt = resp
				if _, err := os.Stdout.WriteString(`{"type":"agent_start"}` + "\n" + `{"type":"agent_settled","aborted":false}` + "\n"); err != nil {
					os.Exit(32)
				}
				continue
			}
			if active {
				if len(req.Images) != 1 || req.Images[0].Type != "image" || req.Images[0].Data != "aGk=" || req.Images[0].MIMEType != "image/png" {
					os.Exit(31)
				}
				resp["data"] = map[string]string{"disposition": "queued"}
				break
			}
			active = true
			resp["data"] = map[string]string{"disposition": "started"}
			events = []string{
				`{"type":"agent_start"}`, `{"type":"message_start","message":{"role":"assistant","content":[]}}`,
				`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hi"}}`,
				`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hi"}],"stopReason":"stop"}}`,
				`{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a"}}`,
				`{"type":"tool_execution_end","toolCallId":"t1","toolName":"read","result":{"content":[]},"isError":false}`,
				`{"type":"future_event","future":{"priority":7,"values":[1,true,null]}}`,
			}
			switch req.Message {
			case "hello":
			case "think and answer":
				active = false
				events = append(events[:2],
					`{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Consider the question."},{"type":"text","text":"Hi"}],"stopReason":"stop"}}`,
					`{"type":"agent_settled","aborted":false}`,
				)
			case "continue":
				active = false
				events = append(events[:2],
					`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Continued."}],"stopReason":"stop"}}`,
					`{"type":"agent_settled","aborted":false}`,
				)
			case "fail":
				active = false
				events = append(events[:2],
					`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"connection lost"}}`,
					`{"type":"agent_end","willRetry":false}`,
					`{"type":"agent_settled","aborted":false}`,
				)
			default:
				os.Exit(22)
			}
		case "abort":
			if !active {
				os.Exit(23)
			}
			active = false
			_, _ = os.Stdout.WriteString(`{"type":"agent_settled","aborted":true}` + "\n")
		default:
			os.Exit(24)
		}
		if err := emit.Encode(resp); err != nil {
			os.Exit(25)
		}
		for _, frame := range events {
			if _, err := os.Stdout.WriteString(frame + "\n"); err != nil {
				os.Exit(32)
			}
		}
	}
	os.Exit(0)
}
