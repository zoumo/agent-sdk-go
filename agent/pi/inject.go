package agentpi

import (
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
)

// injectSession exposes the optional operation after the private extension
// handshake succeeds. Ordinary Pi sessions keep their existing method set.
type injectSession struct{ *session }

var _ agent.Injector = (*injectSession)(nil)

func (s *injectSession) Inject(ctx context.Context, input agent.Input) (receipt agent.InjectReceipt, err error) {
	ctx, observation := s.core.telemetry.Begin(ctx, telemetry.Inject)
	defer func() { observation.EndAcceptance(receipt.Acceptance, err) }()
	receipt.Acceptance.Sent = new(false)

	text, images, err := promptContent(input)
	if err != nil {
		return receipt, err
	}
	blocks := make([]any, 0, len(input.Content))
	if input.Content[0].Text != nil {
		blocks = append(blocks, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: text})
	}
	for _, image := range images {
		blocks = append(blocks, image)
	}
	payload, err := json.Marshal(blocks)
	if err != nil {
		return receipt, operationError("inject", err)
	}

	if err := s.admitInjection(); err != nil {
		return receipt, err
	}
	defer s.finishInjection()

	// Pi routes slash commands before deciding whether to start a run. Do not
	// provide streamingBehavior: SDK injection must never queue a prompt.
	ack, err := s.native.Prompt(ctx, "/"+sessionInjectCommand+" "+string(payload), nil, "")
	if err != nil {
		receipt.Acceptance = commandAcceptance(err)
		return receipt, operationError("inject", err)
	}
	receipt.Acceptance.Sent = new(true)
	if ack.Disposition != "handled" {
		return receipt, &agent.Error{
			Code: agent.ErrorOutcomeUnknown, Reason: "pi_inject_disposition_unknown",
			Message: "Pi did not confirm handling the injection command",
		}
	}
	receipt.Acceptance.NativeAccepted = new(true)
	return receipt, nil
}
