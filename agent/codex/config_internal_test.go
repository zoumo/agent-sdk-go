package agentcodex

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func TestNewCopiesNestedStdioConfiguration(t *testing.T) {
	original := codex.StdioOptions{
		Args: []string{"serve"},
		Env:  []string{"MODE=original"},
		Initialize: codextypes.InitializeParams{
			ClientInfo: codextypes.ClientInfo{
				Name:    "agent-codex-test",
				Version: "1",
				UnknownFields: map[string]json.RawMessage{
					"clientFuture": json.RawMessage(`{"source":"original"}`),
				},
			},
			Capabilities: codextypes.Some(codextypes.InitializeCapabilities{
				Extensions: codextypes.Some(map[string]json.RawMessage{
					"future": json.RawMessage(`{"source":"original"}`),
				}),
				OptOutNotificationMethods: codextypes.Some([]string{"thread/started"}),
			}),
			UnknownFields: map[string]json.RawMessage{
				"initializeFuture": json.RawMessage(`{"source":"original"}`),
			},
		},
	}
	provider := New(Config{Stdio: original})

	original.Args[0] = "mutated"
	original.Env[0] = "MODE=mutated"
	original.Initialize.ClientInfo.UnknownFields["clientFuture"] = json.RawMessage(`{"source":"mutated"}`)
	original.Initialize.UnknownFields["initializeFuture"] = json.RawMessage(`{"source":"mutated"}`)
	capabilities, ok := original.Initialize.Capabilities.Get()
	require.True(t, ok)
	extensions, ok := capabilities.Extensions.Get()
	require.True(t, ok)
	extensions["future"] = json.RawMessage(`{"source":"mutated"}`)
	optOut, ok := capabilities.OptOutNotificationMethods.Get()
	require.True(t, ok)
	optOut[0] = "mutated"

	assert.Equal(t, []string{"serve"}, provider.config.Args)
	assert.Equal(t, []string{"MODE=original"}, provider.config.Env)
	assert.JSONEq(
		t,
		`{"source":"original"}`,
		string(provider.config.Initialize.ClientInfo.UnknownFields["clientFuture"]),
	)
	assert.JSONEq(
		t,
		`{"source":"original"}`,
		string(provider.config.Initialize.UnknownFields["initializeFuture"]),
	)
	frozenCapabilities, ok := provider.config.Initialize.Capabilities.Get()
	require.True(t, ok)
	frozenExtensions, ok := frozenCapabilities.Extensions.Get()
	require.True(t, ok)
	assert.JSONEq(t, `{"source":"original"}`, string(frozenExtensions["future"]))
	frozenOptOut, ok := frozenCapabilities.OptOutNotificationMethods.Get()
	require.True(t, ok)
	assert.Equal(t, []string{"thread/started"}, frozenOptOut)
}
