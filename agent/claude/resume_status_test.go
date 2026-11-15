package agentclaude_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func resumeStatusBody(id, cwd string) string {
	return fmt.Sprintf(`{"sections":[{"title":"Session","rows":[{"label":"Session ID","value":%q},{"label":"cwd","value":%q}]}]}`, id, cwd)
}

func TestProviderResumeStatusDiscardsPrivateRows(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(strconv.FormatBool(malformed), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
			body := resumeStatusBody(claudeResumePeerSessionID, cwd)
			body = body[:len(body)-1] + `,"account":{"private":"STATUS_PRIVATE_SENTINEL"}}`
			if malformed {
				body = `{"sections":[],"sections":[],"account":{"private":"STATUS_PRIVATE_SENTINEL"}}`
			}
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, StatusResponse: body})
			provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
			session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd}})
			if malformed {
				require.Error(t, err)
				assert.Nil(t, session)
				assert.NotContains(t, err.Error(), "STATUS_PRIVATE_SENTINEL")
				var native *claude.Error
				assert.NotErrorAs(t, err, &native)
				return
			}
			require.NoError(t, err)
			closeSessionOnCleanup(t, session)
			stream, err := session.Events()
			require.NoError(t, err)
			require.NoError(t, session.Close(ctx))
			for {
				event, err := stream.Next(ctx)
				if err != nil {
					break
				}
				data, err := json.Marshal(event)
				require.NoError(t, err)
				assert.NotContains(t, string(data), "STATUS_PRIVATE_SENTINEL")
			}
		})
	}
}

func TestProviderResumeConfirmsReadOnlyStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cwd, config := t.TempDir(), newResumeConfigDir(t)
	writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, StatusResponse: resumeStatusBody(claudeResumePeerSessionID, cwd)})
	provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, time.Second)
	session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd}})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	assert.Equal(t, claudeResumePeerSessionID, session.ID())
	require.NoError(t, session.Close(ctx))
	observation := readOpenPeerObservation(t, peer.Observation)
	assert.Empty(t, observation.UserFrames)
}

func TestProviderResumeRejectsInvalidStatus(t *testing.T) {
	for _, name := range []string{"missing", "duplicate id", "duplicate cwd", "empty id", "wrong id", "wrong cwd", "relative cwd", "wrong type", "duplicate key", "wrong request", "native error"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cwd, config := t.TempDir(), newResumeConfigDir(t)
			writeResumeTranscript(t, config, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
			body := resumeStatusBody(claudeResumePeerSessionID, cwd)
			code := agent.ErrorNativeRejected
			switch name {
			case "missing":
				body = `{"sections":[]}`
			case "duplicate id":
				body = fmt.Sprintf(`{"sections":[{"rows":[{"label":"Session ID","value":%q},{"label":"Session ID","value":%q},{"label":"cwd","value":%q}]}]}`, claudeResumePeerSessionID, claudeResumePeerSessionID, cwd)
			case "duplicate cwd":
				body = fmt.Sprintf(`{"sections":[{"rows":[{"label":"Session ID","value":%q},{"label":"cwd","value":%q},{"label":"cwd","value":%q}]}]}`, claudeResumePeerSessionID, cwd, cwd)
			case "empty id":
				body = resumeStatusBody("", cwd)
			case "wrong id":
				body = resumeStatusBody("22222222-2222-4222-8222-222222222222", cwd)
			case "wrong cwd":
				body = resumeStatusBody(claudeResumePeerSessionID, t.TempDir())
			case "relative cwd":
				body = resumeStatusBody(claudeResumePeerSessionID, "relative")
			case "wrong type":
				body = `{"sections":[{"rows":[{"label":"Session ID","value":42}]}]}`
			case "duplicate key":
				body = `{"sections":[],"sections":[]}`
			case "wrong request", "native error":
				code = agent.ErrorUnavailable
			}
			peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, StatusResponse: body, StatusWrongID: name == "wrong request", StatusError: name == "native error"})
			provider := newResumeProvider(t, peer.CLIPath, peer.Env, config, 250*time.Millisecond)
			session, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: agent.OpenOptions{Cwd: cwd}})
			require.Error(t, err)
			assert.Nil(t, session)
			assert.Equal(t, code, agent.CodeOf(err))
		})
	}
}
