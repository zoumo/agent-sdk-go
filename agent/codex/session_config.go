package agentcodex

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

func optionalString(value string) codextypes.Optional[string] {
	if value == "" {
		return codextypes.Optional[string]{}
	}
	return codextypes.Some(value)
}

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

	var err error
	switch change.ID {
	case agent.ConfigModel:
		_, err = s.native.SetModel(ctx, change.Value)
	case agent.ConfigEffort:
		_, err = s.native.SetThinking(ctx, codex.ThinkingChange{
			Effort: codextypes.Some[codextypes.ReasoningEffort](change.Value),
		})
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
	var callErr *codex.CallError
	if !errors.As(err, &callErr) {
		return agent.Acceptance{Sent: &sent}
	}
	switch callErr.Dispatch {
	case codex.NotSent:
		sent = false
		return agent.Acceptance{Sent: &sent}
	case codex.Responded:
		accepted := false
		return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
	default:
		return agent.Acceptance{Sent: &sent}
	}
}

func configNotSent() agent.Acceptance {
	sent := false
	return agent.Acceptance{Sent: &sent}
}

func configAccepted() agent.Acceptance {
	sent, accepted := true, true
	return agent.Acceptance{Sent: &sent, NativeAccepted: &accepted}
}
