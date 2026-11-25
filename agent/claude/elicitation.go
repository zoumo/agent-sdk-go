package agentclaude

import (
	"context"
	"encoding/json"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/client/claude"
)

type elicitationKey struct {
	server string
	id     string
}

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
		elicitation: elicitationKey{server: params.ServerName, id: params.ElicitationID},
	}, unified)
}

func (s *session) completeElicitation(received claude.Received) ([]agent.Event, error) {
	raw := received.Event.Raw
	if len(raw) == 0 {
		raw = received.Raw
	}
	var notice struct {
		SessionID string `json:"session_id"`
		Server    string `json:"mcp_server_name"`
		ID        string `json:"elicitation_id"`
	}
	if err := json.Unmarshal(raw, &notice); err == nil &&
		notice.SessionID == s.ID() && notice.Server != "" && notice.ID != "" {
		key := elicitationKey{server: notice.Server, id: notice.ID}
		interaction, duplicate := s.claimElicitationCompletion(key)
		if duplicate {
			return nil, nil
		}
		if interaction != nil {
			event, err := s.interactionClosedEvent(
				received, interaction, agent.InteractionCompleted, "", nativeEventTime(raw),
			)
			if err != nil {
				return nil, err
			}
			return []agent.Event{event}, nil
		}
	}
	return s.unsupportedNativeEvent(received, "system", "elicitation_complete",
		map[string]any{"eventType": "system", "eventSubtype": "elicitation_complete"})
}

func (s *session) claimElicitationCompletion(key elicitationKey) (*pendingInteraction, bool) {
	for {
		s.mu.Lock()
		if _, duplicate := s.completedURLs[key]; duplicate {
			s.mu.Unlock()
			return nil, true
		}
		var match *pendingInteraction
		for _, interaction := range s.interactions {
			if interaction.elicitation == key {
				if match != nil {
					s.mu.Unlock()
					return nil, false
				}
				match = interaction
			}
		}
		if match != nil && match.urlAnswerDone != nil {
			// The native receive loop can see completion before Answer returns.
			done := match.urlAnswerDone
			s.mu.Unlock()
			<-done
			continue
		}
		if match == nil || !match.acceptedURL {
			s.mu.Unlock()
			return nil, false
		}
		s.deleteInteractionLocked(match)
		if s.completedURLs == nil {
			s.completedURLs = make(map[elicitationKey]struct{})
		}
		s.completedURLs[key] = struct{}{}
		s.mu.Unlock()
		return match, false
	}
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
