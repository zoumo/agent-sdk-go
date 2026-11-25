package agentcodex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/client/codex"
	codextypes "github.com/zoumo/agent-sdk-go/client/codex/types"
)

func (s *session) Inject(ctx context.Context, input agent.Input) (receipt agent.InjectReceipt, err error) {
	ctx, observation := s.provider.telemetry.Begin(ctx, telemetry.Inject)
	defer func() { observation.EndAcceptance(receipt.Acceptance, err) }()
	receipt = agent.InjectReceipt{Acceptance: configNotSent()}
	nativeInput, err := encodeInput(input)
	if err != nil {
		return receipt, err
	}
	item, err := injectMessage(nativeInput.Input)
	if err != nil {
		return receipt, err
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

func injectMessage(input []codextypes.UserInput) (json.RawMessage, error) {
	content := make([]codextypes.ContentItem, 0, len(input))
	for _, part := range input {
		switch {
		case part.Text != nil:
			content = append(content, codextypes.ContentItem{InputText: &codextypes.InputTextContentItem{
				Type: codextypes.InputTextContentItemTypeInputText,
				Text: part.Text.Text,
			}})
		case part.Image != nil:
			detail := part.Image.Detail
			if !detail.IsSet() {
				// turn/start applies this default when converting a UserInput.
				detail = codextypes.Some(codextypes.ImageDetailHigh)
			}
			content = append(content, codextypes.ContentItem{InputImage: &codextypes.InputImageContentItem{
				Type:     codextypes.InputImageContentItemTypeInputImage,
				ImageURL: part.Image.URL,
				Detail:   detail,
				FileID:   part.Image.FileID,
			}})
		case part.Audio != nil:
			content = append(content, codextypes.ContentItem{InputAudio: &codextypes.InputAudioContentItem{
				Type:     codextypes.InputAudioContentItemTypeInputAudio,
				AudioURL: part.Audio.URL,
			}})
		default:
			return nil, adapterError(
				agent.ErrorUnsupported, agent.ErrorReasonContentBlockUnsupported,
				"Codex does not support injecting this input block", nil,
			)
		}
	}
	item, err := json.Marshal(codextypes.MessageResponseItem{
		Type: codextypes.MessageResponseItemTypeMessage, Role: "user", Content: content,
	})
	if err != nil {
		return nil, nativeOperationError("inject", err)
	}
	return item, nil
}
