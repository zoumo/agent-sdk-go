package agent_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

type contractProvider struct{}

func (contractProvider) Start(context.Context) (agent.Capabilities, error) {
	return agent.Capabilities{}, nil
}

func (contractProvider) Open(context.Context, agent.OpenOptions) (agent.Session, error) {
	return nil, nil
}

func (contractProvider) Resume(context.Context, agent.ResumeOptions) (agent.Session, error) {
	return nil, nil
}

func (contractProvider) Stop(context.Context) error {
	return nil
}

type contractSession struct{}

func (contractSession) ID() string {
	return "session"
}

func (contractSession) Capabilities() agent.Capabilities {
	return agent.Capabilities{}
}

func (contractSession) EffectiveFeatures() agent.EffectiveFeatures {
	return agent.EffectiveFeatures{}
}

func (contractSession) Events() (agent.EventStream, error) {
	return contractStream{}, nil
}

func (contractSession) StartOrSteer(context.Context, agent.Input) (agent.InputReceipt, error) {
	return agent.InputReceipt{}, nil
}

func (contractSession) Answer(
	context.Context,
	agent.InteractionRef,
	agent.Answer,
) (agent.InteractionReceipt, error) {
	return agent.InteractionReceipt{}, nil
}

func (contractSession) Interrupt(
	context.Context,
	agent.InterruptOptions,
) (agent.InterruptReceipt, error) {
	return agent.InterruptReceipt{}, nil
}

func (contractSession) Close(context.Context) error {
	return nil
}

func (contractSession) Done() <-chan struct{} {
	return nil
}

func (contractSession) Err() error {
	return nil
}

func (contractSession) Archive(context.Context) (agent.ActionReceipt, error) {
	return agent.ActionReceipt{}, nil
}

func (contractSession) Unarchive(context.Context) (agent.ActionReceipt, error) {
	return agent.ActionReceipt{}, nil
}

type contractConfigController struct{}

func (contractConfigController) SetConfig(
	context.Context,
	agent.ConfigChange,
) (agent.ConfigReceipt, error) {
	return agent.ConfigReceipt{}, nil
}

type contractStream struct{}

func (contractStream) Next(context.Context) (agent.Event, error) {
	return agent.Event{}, nil
}

func TestPublicContractsCanBeImplementedByConsumers(t *testing.T) {
	var provider agent.Core = contractProvider{}
	var session agent.Session = contractSession{}
	var stream agent.EventStream = contractStream{}
	var archiver agent.Archiver = contractSession{}
	var configController agent.ConfigController = contractConfigController{}

	require.NotNil(t, provider)
	require.NotNil(t, session)
	require.NotNil(t, stream)
	require.NotNil(t, archiver)
	require.NotNil(t, configController)

	_, implementsConfig := any(contractSession{}).(agent.ConfigController)
	assert.False(t, implementsConfig)
}

func TestConfigIDsAreFixed(t *testing.T) {
	assert.Equal(t, "model", string(agent.ConfigModel))
	assert.Equal(t, "effort", string(agent.ConfigEffort))
}

func TestUnifiedSessionHasOneInputOperation(t *testing.T) {
	sessionType := reflect.TypeFor[agent.Session]()

	_, hasStartOrSteer := sessionType.MethodByName("StartOrSteer")
	_, hasSubmit := sessionType.MethodByName("Submit")
	_, hasSteer := sessionType.MethodByName("Steer")

	require.True(t, hasStartOrSteer)
	require.False(t, hasSubmit)
	require.False(t, hasSteer)
}

func TestPublicMCPContract(t *testing.T) {
	stdio := agent.MCPServer{
		Name:      "local",
		Transport: agent.MCPTransportStdio,
		Command:   "mcp-local",
		Args:      []string{"--stdio"},
		Env:       map[string]string{"LOCAL_MODE": "read-only"},
		Cwd:       "/synthetic/workspace",
	}
	http := agent.MCPServer{
		Name:              "remote",
		Transport:         agent.MCPTransportHTTP,
		URL:               "https://mcp.example.invalid/tools",
		Headers:           map[string]string{"X-Workspace": "sdk"},
		EnvHeaders:        map[string]string{"Authorization": "REMOTE_AUTHORIZATION"},
		BearerTokenEnvVar: "REMOTE_TOKEN",
	}
	options := agent.OpenOptions{
		Cwd:        "/synthetic/workspace",
		MCPServers: []agent.MCPServer{stdio, http},
	}
	resume := agent.ResumeOptions{
		SessionID:   "session-1",
		OpenOptions: options,
	}

	assert.Equal(t, "stdio", string(agent.MCPTransportStdio))
	assert.Equal(t, "http", string(agent.MCPTransportHTTP))
	assert.Equal(t, options.MCPServers, resume.MCPServers)
	for _, field := range []string{"MCPConfigMode", "StrictMCPConfig", "SDKMCPServers"} {
		_, found := reflect.TypeFor[agent.OpenOptions]().FieldByName(field)
		assert.False(t, found, "unified OpenOptions must not expose %s", field)
	}
}

func TestPublicAdditionalSystemPromptContract(t *testing.T) {
	optionsType := reflect.TypeFor[agent.OpenOptions]()
	field, found := optionsType.FieldByName("AdditionalSystemPrompt")
	require.True(t, found)
	assert.Equal(t, reflect.String, field.Type.Kind())

	_, found = optionsType.FieldByName("SystemPrompt")
	assert.False(t, found, "unified OpenOptions must not retain the old field")

	options := agent.OpenOptions{
		Cwd:                    "/synthetic/workspace",
		AdditionalSystemPrompt: "multi-line\nliteral --leading-dash ünïcødé",
	}
	resume := agent.ResumeOptions{
		SessionID:   "session-1",
		OpenOptions: options,
	}
	assert.Equal(t, options.AdditionalSystemPrompt, resume.AdditionalSystemPrompt)
}

func TestConfigReceiptHasOnlyChangeAndAcceptance(t *testing.T) {
	receiptType := reflect.TypeFor[agent.ConfigReceipt]()
	require.Equal(t, 2, receiptType.NumField())
	assert.Equal(t, "Change", receiptType.Field(0).Name)
	assert.Equal(t, "Acceptance", receiptType.Field(1).Name)

	controllerType := reflect.TypeFor[agent.ConfigController]()
	require.Equal(t, 1, controllerType.NumMethod())
	_, hasSetConfig := controllerType.MethodByName("SetConfig")
	assert.True(t, hasSetConfig)
}
