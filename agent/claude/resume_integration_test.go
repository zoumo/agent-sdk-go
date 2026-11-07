package agentclaude_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestResumeConfirmsNativeIdentityAndDropsReplayContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames,
		`<{"type":"system","subtype":"init","session_id":"resume-target","cwd":`+
			jsonString(t, cwd)+`,"claude_code_version":"2.1.258"}`,
		`<{"type":"assistant","session_id":"resume-target","isReplay":true,"message":{"id":"saved-message","content":[{"type":"text","text":"saved"}]}}`,
		`<{"type":"assistant","session_id":"resume-target","message":{"id":"live-message","content":[{"type":"text","text":"live","apiBlockIndex":0}]}}`,
	)
	peerSession := prepareClaudePeer(t, "passed", frames...)
	provider := newTestClaudeProvider(t, peerSession)
	t.Cleanup(func() { assert.NoError(t, provider.Stop(context.Background())) })

	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Resume(ctx, agent.ResumeOptions{
		SessionID:   "resume-target",
		OpenOptions: agent.OpenOptions{Cwd: cwd},
	})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, "resume-target", session.ID())

	stream, err := session.Events()
	require.NoError(t, err)
	event, err := stream.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.EventAgentMessage, event.Type())
	assert.Equal(t, "live-message:0", event.ItemID)
}

func TestResumeRejectsMismatchedInitIdentityAndCWD(t *testing.T) {
	tests := []struct {
		name string
		init func(t *testing.T, cwd string) string
	}{
		{
			name: "identity",
			init: func(t *testing.T, cwd string) string {
				return `{"type":"system","subtype":"init","session_id":"different","cwd":` +
					jsonString(t, cwd) + `,"claude_code_version":"2.1.258"}`
			},
		},
		{
			name: "cwd",
			init: func(t *testing.T, _ string) string {
				return `{"type":"system","subtype":"init","session_id":"resume-target","cwd":` +
					jsonString(t, t.TempDir()) + `,"claude_code_version":"2.1.258"}`
			},
		},
		{
			name: "replay",
			init: func(t *testing.T, cwd string) string {
				return `{"type":"system","subtype":"init","session_id":"resume-target","isReplay":true,"cwd":` +
					jsonString(t, cwd) + `,"claude_code_version":"2.1.258"}`
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cwd := t.TempDir()
			frames := append([]string(nil), mockClaudeInit...)
			frames = append(frames, "<"+tt.init(t, cwd))
			peerSession := prepareClaudePeer(t, "passed", frames...)
			provider := newTestClaudeProvider(t, peerSession)
			t.Cleanup(func() {
				assert.NoError(t, provider.Stop(context.Background()))
			})

			_, err := provider.Start(ctx)
			require.NoError(t, err)
			_, err = provider.Resume(ctx, agent.ResumeOptions{
				SessionID:   "resume-target",
				OpenOptions: agent.OpenOptions{Cwd: cwd},
			})
			require.Error(t, err)
			assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(err))
		})
	}
}
