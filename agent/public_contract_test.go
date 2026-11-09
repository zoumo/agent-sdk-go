package agent_test

import (
	"context"
	"testing"

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

func (contractSession) Submit(context.Context, agent.Input) (agent.InputReceipt, error) {
	return agent.InputReceipt{}, nil
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

	require.NotNil(t, provider)
	require.NotNil(t, initial)
	require.NotNil(t, session)
	require.NotNil(t, stream)
	require.NotNil(t, archiver)
	require.NotNil(t, modelController)
}
