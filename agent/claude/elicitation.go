package agentclaude

import (
	"context"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

func (s *session) elicitationInteraction(received claude.Received, request claude.IncomingRequest) ([]agent.Event, error) {
	params, err := request.Elicitation()
	if err != nil {
		return s.rejectUnsupportedRequest(received, "native_request_invalid",
			"Claude MCP elicitation has invalid or unsupported parameters", "")
	}
	unified := agent.InteractionRequest{Message: params.Message}
	if params.Mode == "url" {
		unified.Kind = agent.InteractionURL
		unified.URL = params.URL
	} else {
		unified.Kind = agent.InteractionForm
		unified.RequestedSchema = params.RequestedSchema
		if unified.RequestedSchema == nil {
			// The native schema is optional. An empty schema imposes no field constraints.
			unified.RequestedSchema = map[string]any{}
		}
	}
	return s.registerInteraction(&pendingInteraction{
		kind: unified.Kind, requestID: request.ID(), request: request,
		received: received, startedAt: nativeEventTime(request.Raw()),
	}, unified)
}

func elicitationReply(request claude.IncomingRequest, action string, content map[string]any) (
	func(context.Context) error, agent.InteractionOutcome, error,
) {
	outcome := agent.InteractionAnswered
	if action == "cancel" {
		outcome = agent.InteractionCancelled
	}
	return func(ctx context.Context) error {
		return request.ReplyElicitation(ctx, action, content)
	}, outcome, nil
}
