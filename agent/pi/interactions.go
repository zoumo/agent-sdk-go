package agentpi

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventenvelope"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/agent/internal/interactionledger"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/client/pi"
)

const (
	sdkApprovalTitle       = "Agent SDK tool approval"
	sdkQuestionTitlePrefix = "Agent SDK question: "
	sdkQuestionTool        = "agent_sdk_ask"
)

type piInteraction struct {
	ref        agent.InteractionRef
	sequence   uint64
	nativeID   string
	toolCallID string
	kind       agent.InteractionKind
	choices    []string
	deadline   time.Time
	slot       interactionledger.Slot
}

func (s *session) registerUI(native pi.Event) ([]agent.Payload, string, string, error) {
	dialog, err := native.UIRequest()
	if err != nil || dialog.TimeoutMS <= 0 || dialog.TimeoutMS > math.MaxInt32 {
		return nil, "", "", nil
	}
	request, toolCallID := s.uiInteraction(dialog)
	if toolCallID == "" {
		return nil, "", "", nil
	}
	ref, sequence, err := s.interactionIDs.Allocate()
	if err != nil {
		return nil, "", "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active == "" {
		return nil, "", "", nil
	}
	for _, pending := range s.interactions {
		if pending.nativeID == dialog.ID {
			return nil, "", "", nil
		}
	}
	s.interactions[ref] = &piInteraction{
		ref: ref, sequence: sequence, nativeID: dialog.ID, toolCallID: toolCallID, kind: request.Kind,
		choices: dialog.Options, deadline: time.Now().Add(time.Duration(dialog.TimeoutMS) * time.Millisecond),
	}
	payloads := make([]agent.Payload, 0, 2)
	if s.state != agent.StateRequiresAction {
		previous, next := s.state, agent.StateRequiresAction
		s.state = next
		payloads = append(payloads, agent.StateUpdatePayload{PreviousState: &previous, State: &next})
	}
	payloads = append(payloads, agent.InteractionRequestedPayload{Interaction: ref, Request: request, Blocking: true})
	return payloads, toolCallID, dialog.ID, nil
}

func (s *session) uiInteraction(dialog pi.UIRequest) (agent.InteractionRequest, string) {
	if s.toolApproval && dialog.Method == "confirm" && dialog.Title == sdkApprovalTitle {
		var body struct{ Tool, ToolCallID, Detail string }
		if json.Unmarshal([]byte(dialog.Message), &body) != nil || !isApprovalTool(body.Tool) {
			return agent.InteractionRequest{}, ""
		}
		request := agent.InteractionRequest{
			Kind: agent.InteractionPermission, ToolCallID: body.ToolCallID, Title: "Approve Pi " + body.Tool,
			Options: []agent.PermissionOption{
				{OptionID: "allow", Name: "Allow once", Kind: agent.PermissionAllow, Scope: agent.PermissionScopeOnce},
				{OptionID: "deny", Name: "Deny", Kind: agent.PermissionReject, Scope: agent.PermissionScopeOnce},
			},
		}
		if body.Detail != "" {
			request.Content = textContent(body.Detail)
		}
		return request, body.ToolCallID
	}
	if !s.features.AgentQuestions || (dialog.Method != "select" && dialog.Method != "input") {
		return agent.InteractionRequest{}, ""
	}
	raw, ok := strings.CutPrefix(dialog.Title, sdkQuestionTitlePrefix)
	if !ok {
		return agent.InteractionRequest{}, ""
	}
	var body struct{ Question, ToolCallID string }
	if json.Unmarshal([]byte(raw), &body) != nil || body.Question == "" {
		return agent.InteractionRequest{}, ""
	}
	value := map[string]any{"type": "string"}
	if dialog.Method == "select" {
		if len(dialog.Options) < 2 {
			return agent.InteractionRequest{}, ""
		}
		value["enum"] = slices.Clone(dialog.Options)
	}
	return agent.InteractionRequest{
		Kind: agent.InteractionForm, Message: body.Question,
		RequestedSchema: map[string]any{
			"type": "object", "properties": map[string]any{"value": value},
			"required": []string{"value"}, "additionalProperties": false,
		},
	}, body.ToolCallID
}

func isApprovalTool(name string) bool {
	return name == "write" || name == "edit" || name == "bash" || name == "powershell"
}

func (s *session) Answer(ctx context.Context, ref agent.InteractionRef, answer agent.Answer) (receipt agent.InteractionReceipt, err error) {
	ctx, observation := s.core.telemetry.Begin(ctx, telemetry.Answer)
	defer func() { observation.EndAcceptance(receipt.Acceptance, err) }()
	interaction, response, outcome, err := s.admitAnswer(ref, answer)
	if err != nil {
		return receipt, err
	}
	writeContext, cancel := context.WithDeadline(ctx, interaction.deadline)
	defer cancel()
	err = s.native.RespondUI(writeContext, response)
	receipt = agent.InteractionReceipt{Interaction: ref, Acceptance: agent.Acceptance{Sent: new(false)}}
	if err == nil {
		interaction.slot.CompleteSent(outcome)
		receipt.Acceptance.Sent = new(true)
		return receipt, nil
	}
	var dispatch *pi.DispatchError
	if errors.As(err, &dispatch) {
		interaction.slot.CompleteTerminal(agent.InteractionFailed, agent.ErrorReasonAnswerOutcomeUnknown)
		receipt.Acceptance = commandAcceptance(err)
	} else {
		interaction.slot.ReleaseNotSent()
	}
	return receipt, operationError("answer", err)
}

func (s *session) admitAnswer(ref agent.InteractionRef, answer agent.Answer) (*piInteraction, pi.UIResponse, agent.InteractionOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reject := func(err error) (*piInteraction, pi.UIResponse, agent.InteractionOutcome, error) {
		return nil, pi.UIResponse{}, "", err
	}
	if s.closed {
		return reject(closedError())
	}
	if !s.toolApproval && !s.features.AgentQuestions {
		return reject(unsupported(agent.ErrorReasonUnsupportedAnswer, "Pi session has no configured SDK interactions"))
	}
	interaction := s.interactions[ref]
	if interaction == nil {
		return reject(&agent.Error{Code: agent.ErrorInvalidArgument, Reason: agent.ErrorReasonInvalidInteractionRef, Message: "Pi interaction reference is not live in this session"})
	}
	if interaction.slot.State() != interactionledger.Pending || !time.Now().Before(interaction.deadline) {
		return reject(&agent.Error{Code: agent.ErrorFailedPrecondition, Reason: agent.ErrorReasonInteractionNotPending, Message: "Pi interaction is no longer pending"})
	}
	matched, err := agent.MatchAnswer(interaction.kind, answer)
	if err != nil {
		return reject(err)
	}
	response, outcome, err := interaction.reply(matched)
	if err != nil {
		return reject(err)
	}
	interaction.slot.Claim()
	return interaction, response, outcome, nil
}

func (i *piInteraction) reply(answer agent.Answer) (pi.UIResponse, agent.InteractionOutcome, error) {
	response := pi.UIResponse{ID: i.nativeID}
	outcome := agent.InteractionAnswered
	switch value := answer.(type) {
	case agent.PermissionAnswer:
		switch {
		case value.Outcome == agent.PermissionCancelled:
			response.Canceled = true
			outcome = agent.InteractionCancelled
		case value.OptionID == "allow" || value.OptionID == "deny":
			response.Confirmed = new(value.OptionID == "allow")
		default:
			return response, "", &agent.Error{Code: agent.ErrorInvalidArgument, Reason: agent.ErrorReasonPermissionOptionNotOffered, Message: "Pi permission option was not offered"}
		}
	case agent.FormAnswer:
		if value.Outcome != agent.FormAccept {
			response.Canceled = true
			outcome = agent.InteractionCancelled
			break
		}
		selected, ok := value.Content["value"].(string)
		if !ok || len(value.Content) != 1 || (i.choices != nil && !slices.Contains(i.choices, selected)) {
			return response, "", &agent.Error{Code: agent.ErrorInvalidArgument, Reason: agent.ErrorReasonInvalidFormAnswer, Message: "Pi form requires exactly one valid string value"}
		}
		response.Value = &selected
	default:
		return response, "", errors.New("pi: unexpected interaction answer kind")
	}
	return response, outcome, nil
}

func (s *session) toolInteractionClosure(id, name string, raw json.RawMessage, isError bool, duration *float64) []agent.Payload {
	if id == "" || (!s.toolApproval && !s.features.AgentQuestions) {
		return nil
	}
	var result struct {
		Content []struct{ Text string }
		Details struct{ SDKInteractionExpired bool }
	}
	_ = json.Unmarshal(raw, &result)
	expired := name == sdkQuestionTool && result.Details.SDKInteractionExpired
	if isApprovalTool(name) && isError && duration == nil && len(result.Content) == 1 {
		expired = result.Content[0].Text == "Agent SDK tool approval expired"
	}
	return s.settleInteractions(id, agent.ErrorReasonNativeRequestEnded, expired, true)
}

func (s *session) settleInteractions(toolCallID, fallbackReason string, expired, resumeRunning bool) []agent.Payload {
	s.mu.Lock()
	var pending []*piInteraction
	for ref, interaction := range s.interactions {
		if toolCallID == "" || interaction.toolCallID == toolCallID {
			delete(s.interactions, ref)
			pending = append(pending, interaction)
		}
	}
	var transition *agent.StateUpdatePayload
	if len(pending) != 0 && len(s.interactions) == 0 && resumeRunning && !s.closed && s.state == agent.StateRequiresAction {
		previous, next := s.state, agent.StateRunning
		s.state = next
		transition = &agent.StateUpdatePayload{PreviousState: &previous, State: &next}
	}
	s.mu.Unlock()
	slices.SortFunc(pending, func(a, b *piInteraction) int { return cmp.Compare(a.sequence, b.sequence) })
	payloads := make([]agent.Payload, 0, len(pending)+1)
	for _, interaction := range pending {
		if expired {
			interaction.slot.CompleteTerminal(agent.InteractionExpired, "pi_ui_request_expired")
		}
		settled := interaction.slot.Resolve(interactionledger.Result{Outcome: agent.InteractionCancelled, Reason: fallbackReason}, "pi_ui_response_sent")
		payloads = append(payloads, agent.InteractionClosedPayload{Interaction: interaction.ref, Outcome: settled.Outcome, Reason: settled.Reason})
	}
	if transition != nil {
		payloads = append(payloads, *transition)
	}
	return payloads
}

func (s *session) closeInteractionEvents(end eventstream.End) []agent.Event {
	reason := agent.ErrorReasonSessionTerminated
	if end.Explicit {
		reason = agent.ErrorReasonSessionClosed
	}
	payloads := s.settleInteractions("", reason, false, false)
	envelope := eventenvelope.Envelope{SessionID: s.ID()}
	events := make([]agent.Event, 0, len(payloads))
	for _, payload := range payloads {
		event, err := envelope.Event("", payload, time.Time{}, nil)
		if err != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}
