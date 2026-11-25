package e2e_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	claudetest "github.com/zoumo/agent-sdk-go/internal/testsupport/claude"
)

func TestUnifiedOpenBeforeInput(t *testing.T) {
	cliPath, model, env := claudetest.AssignedSessionLiveEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	provider := agentclaude.New(agentclaude.Config{CLIPath: cliPath, Env: env})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	session, err := provider.Open(ctx, agent.OpenOptions{
		Cwd: t.TempDir(), Config: agent.SessionConfig{Model: model},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	initialID := session.ID()
	require.NotEmpty(t, initialID)
	t.Logf("Open before user input: session_id=%s", initialID)
	stream, err := session.Events()
	require.NoError(t, err)
	receipt, err := session.StartOrSteer(ctx, agent.Input{
		Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "Reply with SDK67_ACP_OK only. Do not use tools."}}},
	})
	require.NoError(t, err)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.True(t, *receipt.Acceptance.Sent)
	require.Nil(t, receipt.Acceptance.NativeAccepted)
	seenInit, seenAnswer, seenResult, seenRunning := false, false, false, false
	seenCoverageGap := false
	for {
		event, err := stream.Next(ctx)
		require.NoError(t, err)
		require.Equal(t, initialID, event.SessionID)
		if payload, ok := event.Payload.(agent.SessionErrorPayload); ok && payload.Reason == "native_relationship_gap" {
			meta, ok := event.Meta["claude"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "forwarding-not-confirmed", meta["observationReason"])
			require.Equal(t, "initialize-forwardSubagentText-is-a-request", meta["evidence"])
			require.False(t, seenCoverageGap)
			seenCoverageGap = true
			continue
		}
		nativeType, nativeSubtype, raw := claudeNativeEventEvidence(t, event)
		if nativeType == "system" && nativeSubtype == "init" {
			var init struct {
				SessionID         string `json:"session_id"`
				ClaudeCodeVersion string `json:"claude_code_version"`
			}
			require.NoError(t, json.Unmarshal(raw, &init))
			require.Equal(t, initialID, init.SessionID)
			require.NotEmpty(t, init.ClaudeCodeVersion)
			seenInit = true
			t.Logf("unified init matched session_id=%s claude_code_version=%s", init.SessionID, init.ClaudeCodeVersion)
		}
		if event.Type() == agent.EventAgentMessage {
			var native struct {
				SessionID string `json:"session_id"`
			}
			require.NoError(t, json.Unmarshal(raw, &native))
			require.Equal(t, initialID, native.SessionID)
			seenAnswer = true
		}
		if nativeType == "result" {
			var result struct {
				SessionID string `json:"session_id"`
				Subtype   string `json:"subtype"`
				IsError   bool   `json:"is_error"`
			}
			require.NoError(t, json.Unmarshal(raw, &result))
			require.Equal(t, initialID, result.SessionID)
			require.Equal(t, "success", result.Subtype)
			require.False(t, result.IsError)
			t.Logf("unified result matched session_id=%s subtype=%s is_error=%t", result.SessionID, result.Subtype, result.IsError)
			require.True(t, seenInit, "successful result arrived without a matching native init")
			require.True(t, seenAnswer, "successful result arrived without a matching assistant message")
			require.True(t, seenCoverageGap, "requested forwarding coverage gap was not delivered")
			seenResult = true
		}
		if payload, ok := event.Payload.(agent.StateUpdatePayload); ok {
			require.Equal(t, "system", nativeType)
			require.Equal(t, "session_state_changed", nativeSubtype)
			require.NotNil(t, payload.State)
			t.Logf("native session_state_changed: state=%s", *payload.State)
			if *payload.State == agent.StateRunning {
				seenRunning = true
			}
			if *payload.State == agent.StateIdle && seenResult {
				require.True(t, seenRunning, "turn completed without a native running notification")
				break
			}
		}
		if payload, ok := event.Payload.(agent.UnsupportedNativeEventPayload); ok {
			require.Equal(t, "claude", payload.AgentType)
			t.Logf("unified mapping gap: type=%s subtype=%s", nativeType, nativeSubtype)
		}
		if payload, ok := event.Payload.(agent.SessionErrorPayload); ok {
			t.Fatalf("unexpected unified session error: %s (%s)", payload.Message, payload.Reason)
		}
	}
	interrupt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	require.Equal(t, agent.InterruptReceipt{Result: agent.InterruptNoActiveWork}, interrupt)
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.Err())
	require.Equal(t, initialID, session.ID())
	require.NoError(t, provider.Stop(ctx))
	t.Log("one unified input produced matching init, assistant, success result and native running/idle; Interrupt reported no active work; Close and Stop succeeded")
}

func claudeNativeEventEvidence(t *testing.T, event agent.Event) (string, string, json.RawMessage) {
	t.Helper()
	meta, ok := event.Meta["claude"].(map[string]any)
	require.True(t, ok)
	eventType, _ := meta["type"].(string)
	subtype, _ := meta["subtype"].(string)
	if payload, unsupported := event.Payload.(agent.UnsupportedNativeEventPayload); unsupported {
		require.NotContains(t, meta, "raw")
		require.NotEmpty(t, payload.Raw)
		return eventType, subtype, payload.Raw
	}
	raw, ok := meta["raw"].(json.RawMessage)
	require.True(t, ok)
	return eventType, subtype, raw
}
