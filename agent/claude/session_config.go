package agentclaude

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func (s *session) SetConfig(
	ctx context.Context,
	change agent.ConfigChange,
) (agent.ConfigReceipt, error) {
	receipt := agent.ConfigReceipt{
		Change:     change,
		Acceptance: configNotSent(),
	}
	if err := change.Validate(); err != nil {
		return receipt, err
	}
	if err := s.requireOpen(); err != nil {
		return receipt, err
	}

	value := change.Value
	var err error
	switch change.ID {
	case agent.ConfigModel:
		_, err = s.native.SetModel(ctx, claude.ModelChange{Model: &value})
	case agent.ConfigEffort:
		_, err = s.native.SetEffort(ctx, claude.EffortChange{Level: &value})
	default:
		panic("agent.ConfigChange.Validate admitted an unknown ID")
	}
	if err != nil {
		receipt.Acceptance = configAcceptance(err)
		return receipt, nativeOperationError("set_config", err)
	}
	receipt.Acceptance = configAccepted()
	return receipt, nil
}

func configAcceptance(err error) agent.Acceptance {
	sent := true
	var dispatch *claude.DispatchError
	if errors.As(err, &dispatch) && dispatch.State == claude.NotSent {
		sent = false
		return agent.Acceptance{Sent: &sent}
	}
	var control *claude.ControlError
	if errors.As(err, &control) {
		accepted := false
		return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
	}
	return agent.Acceptance{Sent: &sent}
}

func configNotSent() agent.Acceptance {
	sent := false
	return agent.Acceptance{Sent: &sent}
}

func configAccepted() agent.Acceptance {
	sent, accepted := true, true
	return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
}

func (s *session) requireOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return adapterError(
			agent.ErrorClosed,
			"session_closed",
			"session is closed",
			nil,
		)
	}
	return nil
}
