package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestExplicitNativeInitializationPreservesOmissionAndAgents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *types.InitializeOptions
		want    string
	}{
		{"default", nil, `{"subtype":"initialize","hooks":null,"forwardSubagentText":true}`},
		{"omitted", &types.InitializeOptions{}, `{"subtype":"initialize","hooks":null}`},
		{"agents", &types.InitializeOptions{Agents: json.RawMessage(`{"reader":{"prompt":"read fixture","background":false}}`)}, `{"subtype":"initialize","hooks":null,"agents":{"reader":{"prompt":"read fixture","background":false}}}`},
		{"system prompt with hooks omitted", &types.InitializeOptions{SystemPrompt: []string{""}, OmitHooks: true}, `{"subtype":"initialize","systemPrompt":[""]}`},
		{"explicit hooks with omission enabled", &types.InitializeOptions{Hooks: json.RawMessage(`null`), OmitHooks: true}, `{"subtype":"initialize","hooks":null}`},
		{"SDK MCP manifest", &types.InitializeOptions{OmitHooks: true, SDKMCPServerManifests: map[string]types.SDKMCPServerManifest{
			"fixture": {InitializeResult: json.RawMessage(`{"protocolVersion":"2025-11-25","future":9007199254740993}`)},
		}}, `{"subtype":"initialize","sdkMcpServers":["fixture","live"],"sdkMcpServerManifests":{"fixture":{"initializeResult":{"protocolVersion":"2025-11-25","future":9007199254740993}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := fixtureOptions(t, "echo")
			capture := filepath.Join(opts.WorkDir, "capture.json")
			opts.Env = append(opts.Env, "CLAUDE_GO_CAPTURE="+capture)
			opts.Initialize = tc.options
			opts.SDKMCPServers = []string{"fixture", "live"}
			s, err := Open(context.Background(), SessionOptions(opts))
			require.NoError(t, err)
			defer s.Close()
			b, err := os.ReadFile(capture)
			require.NoError(t, err)
			var v struct {
				Init struct{ Request json.RawMessage }
			}
			require.NoError(t, json.Unmarshal(b, &v))
			require.JSONEq(t, tc.want, string(v.Init.Request))
		})
	}
}

func TestInvalidSDKMCPManifestsRejectedBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		server   string
		manifest types.SDKMCPServerManifest
	}{
		{"unknown server", "other", types.SDKMCPServerManifest{InitializeResult: json.RawMessage(`{}`)}},
		{"missing initialize result", "fixture", types.SDKMCPServerManifest{}},
		{"null tools result", "fixture", types.SDKMCPServerManifest{InitializeResult: json.RawMessage(`{}`), ToolsListResult: json.RawMessage(`null`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(t.Context(), SessionOptions{
				CLIPath:       "/must-not-start",
				SDKMCPServers: []string{"fixture"},
				Initialize: &types.InitializeOptions{SDKMCPServerManifests: map[string]types.SDKMCPServerManifest{
					tc.server: tc.manifest,
				}},
			})
			var dispatch *DispatchError
			require.ErrorAs(t, err, &dispatch)
			require.Equal(t, NotSent, dispatch.State)
			require.ErrorIs(t, err, ErrInvalidInput)
		})
	}
}

func TestOfficialDefaultInputAliasPreservesOwnedStream(t *testing.T) {
	s, path := openInputConfigSession(t)
	var user types.UserInput
	require.NoError(t, json.Unmarshal([]byte(`{"type":"user","session_id":"default","parent_tool_use_id":null,"message":{"role":"user","content":"hello"}}`), &user))
	sub, err := s.Submit(context.Background(), Input{User: &user})
	require.NoError(t, err)
	require.True(t, sub.Written)
	receipt, err := s.SetPermissionMode(context.Background(), PermissionModePlan)
	require.NoError(t, err)
	require.True(t, receipt.Accepted)
	require.NoError(t, s.Close())
	frames := permissionTranscript(t, path)
	require.Len(t, frames, 2)
	var input struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(frames[0], &input))
	require.Equal(t, "default", input.SessionID)
	require.JSONEq(t, `{"subtype":"set_permission_mode","mode":"plan"}`, string(receipt.Requested))
}
