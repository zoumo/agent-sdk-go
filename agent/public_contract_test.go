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

func (contractProvider) ValidateInitialInput(agent.Input) error {
	return nil
}

func (contractProvider) OpenWithInput(
	context.Context,
	agent.OpenOptions,
	agent.Input,
) (agent.Session, error) {
	return nil, nil
}

func (contractProvider) ResumeWithInput(
	context.Context,
	agent.ResumeOptions,
	agent.Input,
) (agent.Session, error) {
	return nil, nil
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

func (contractSession) SetModel(
	context.Context,
	string,
) (agent.ConfigurationReceipt, error) {
	return agent.ConfigurationReceipt{}, nil
}

type contractExecutionReporter struct{}

func (contractExecutionReporter) EffectiveExecutionOptions() agent.ExecutionOptionsReport {
	return agent.ExecutionOptionsReport{}
}

type contractExecutionController struct {
	contractExecutionReporter
}

func (contractExecutionController) SetExecutionOption(
	context.Context,
	agent.ExecutionOptionChange,
) (agent.ExecutionOptionsReceipt, error) {
	return agent.ExecutionOptionsReceipt{}, nil
}

type contractExecutionRuntime struct {
	contractExecutionReporter
}

func (contractExecutionRuntime) Lock()   {}
func (contractExecutionRuntime) Unlock() {}

func (contractExecutionRuntime) RequireOpen() error { return nil }

func (contractExecutionRuntime) Apply(
	context.Context,
	agent.ExecutionOptionChange,
) error {
	return nil
}

func (contractExecutionRuntime) ConvertError(err error) error { return err }

func (contractExecutionRuntime) PrepareAccepted(
	agent.ExecutionOptionChange,
) func() {
	return func() {}
}

func (contractExecutionRuntime) ClearUncertain(agent.ExecutionOption) {}

func (contractExecutionRuntime) Acceptance(error) agent.Acceptance {
	return agent.Acceptance{}
}

type contractStream struct{}

func (contractStream) Next(context.Context) (agent.Event, error) {
	return agent.Event{}, nil
}

func TestPublicContractsCanBeImplementedByConsumers(t *testing.T) {
	var provider agent.Provider = contractProvider{}
	var initial agent.InitialInputProvider = contractProvider{}
	var session agent.Session = contractSession{}
	var stream agent.EventStream = contractStream{}
	var archiver agent.Archiver = contractSession{}
	var modelController agent.ModelController = contractSession{}
	var executionReporter agent.ExecutionOptionsReporter = contractExecutionReporter{}
	var executionController agent.ExecutionOptionsController = contractExecutionController{}

	require.NotNil(t, provider)
	require.NotNil(t, initial)
	require.NotNil(t, session)
	require.NotNil(t, stream)
	require.NotNil(t, archiver)
	require.NotNil(t, modelController)
	require.NotNil(t, executionReporter)
	require.NotNil(t, executionController)

	_, implementsReporter := any(contractSession{}).(agent.ExecutionOptionsReporter)
	assert.False(t, implementsReporter)
	_, implementsController := any(contractSession{}).(agent.ExecutionOptionsController)
	assert.False(t, implementsController)
}

func TestExecutionOptionIDsAreFixed(t *testing.T) {
	assert.Equal(t, "model", string(agent.ExecutionOptionModel))
	assert.Equal(t, "reasoningEffort", string(agent.ExecutionOptionReasoningEffort))
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

func TestAdapterExecutionOptionRuntimeContracts(t *testing.T) {
	var runtime agent.ExecutionOptionsRuntime = contractExecutionRuntime{}
	change := agent.ExecutionOptionChange{
		Option: agent.ExecutionOptionReasoningEffort,
		Value:  "high",
	}

	receipt, err := agent.SetExecutionOption(context.Background(), change, runtime)
	require.NoError(t, err)
	assert.Equal(t, change, receipt.Change)
	require.NotNil(t, receipt.Acceptance.Sent)
	require.NotNil(t, receipt.Acceptance.NativeAccepted)
	assert.True(t, *receipt.Acceptance.Sent)
	assert.True(t, *receipt.Acceptance.NativeAccepted)

	modelReceipt, err := agent.SetExecutionModel(
		context.Background(),
		"model",
		runtime,
	)
	require.NoError(t, err)
	assert.Equal(t, "model", modelReceipt.Model)
	require.NotNil(t, modelReceipt.Acceptance.Sent)
	require.NotNil(t, modelReceipt.Acceptance.NativeAccepted)
}
