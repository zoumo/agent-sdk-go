package agentcodex_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func TestAgentQuestionsFeatureControlsNativeThreadConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-open")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-open"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
		`>{"id":94,"method":"thread/resume","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic","excludeTurns":true,"threadId":"root-resume"}}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root-resume")+`}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root-resume"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	peerSession := prepareCodexPeer(t, frames...)
	provider := newTestProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	disabled := false
	features := agent.Features{AgentQuestions: &disabled}

	opened, err := provider.Open(ctx, agent.OpenOptions{
		Cwd:      "/synthetic",
		Features: features,
	})
	require.NoError(t, err)
	assert.Equal(t, agent.EffectiveFeatures{AgentQuestions: false}, opened.EffectiveFeatures())
	assert.False(t, opened.Capabilities().Supports(agent.CapabilityForm))
	require.NoError(t, opened.Close(ctx))

	resumed, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "root-resume",
		OpenOptions: agent.OpenOptions{Cwd: "/synthetic", Features: features},
	})
	require.NoError(t, err)
	assert.Equal(t, agent.EffectiveFeatures{AgentQuestions: false}, resumed.EffectiveFeatures())
	require.NoError(t, resumed.Close(ctx))
}

func TestProviderPreservesExplicitExperimentalAPIFalse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	peerSession := prepareCodexPeer(t,
		`>{"id":91,"method":"initialize","params":{"capabilities":{"experimentalApi":false},"clientInfo":{"name":"agent-codex-test","version":"1"}}}`,
		`<{"id":91,"result":{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}}`,
		`>{"method":"initialized"}`,
		`>{"id":92,"method":"thread/start","params":{"config":{"tools.experimental_request_user_input.enabled":false},"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root-a"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
	)
	disabled := false
	provider := newTestProviderWithInitialize(t, peerSession, codextypes.InitializeParams{
		ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
		Capabilities: codextypes.Some(codextypes.InitializeCapabilities{
			ExperimentalAPI: codextypes.Some(disabled),
		}),
	})
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	capabilities, err := provider.Start(ctx)
	require.NoError(t, err)
	assert.True(t, capabilities.Supports(agent.CapabilityTextInput))
	assert.True(t, capabilities.Supports(agent.CapabilityPermission))
	assert.False(t, capabilities.Supports(agent.CapabilityForm))

	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	assert.Equal(t, agent.EffectiveFeatures{AgentQuestions: false}, session.EffectiveFeatures())
	assert.False(t, session.Capabilities().Supports(agent.CapabilityForm))
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
}
