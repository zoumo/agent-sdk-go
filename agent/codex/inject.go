package agentcodex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

func (s *session) Inject(ctx context.Context, input agent.Input) (agent.InjectReceipt, error) {
	receipt := agent.InjectReceipt{Acceptance: configNotSent()}
	nativeInput, err := textInput(input)
	if err != nil {
		return receipt, err
	}
	content := make([]map[string]string, 0, len(nativeInput.Input))
	for _, part := range nativeInput.Input {
		content = append(content, map[string]string{"type": "input_text", "text": part.Text.Text})
	}
	item, err := json.Marshal(map[string]any{
		"type": "message", "role": "user", "content": content,
	})
	if err != nil {
		return receipt, nativeOperationError("inject", err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return receipt, adapterError(
			agent.ErrorClosed, agent.ErrorReasonSessionClosed, "session is closed", nil,
		)
	}
	if s.submitting || s.injecting || s.submitUnknown {
		s.mu.Unlock()
		return receipt, adapterError(
			agent.ErrorFailedPrecondition, agent.ErrorReasonForCode("inject", agent.ErrorFailedPrecondition),
			"another input is in flight or its outcome is unknown", nil,
		)
	}
	s.injecting = true
	s.mu.Unlock()

	injectErr := s.native.Inject(ctx, []json.RawMessage{item})
	s.mu.Lock()
	s.injecting = false
	s.mu.Unlock()
	if injectErr != nil {
		receipt.Acceptance = configAcceptance(injectErr)
		var rpcErr *codex.RPCError
		if errors.As(injectErr, &rpcErr) && rpcErr.Code == -32601 {
			return receipt, adapterError(
				agent.ErrorUnsupported, agent.ErrorReasonForCode("inject", agent.ErrorUnsupported),
				"Codex does not support thread/inject_items", injectErr,
			)
		}
		return receipt, nativeOperationError("inject", injectErr)
	}
	receipt.Acceptance = configAccepted()
	return receipt, nil
}
