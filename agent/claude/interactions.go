package agentclaude

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"sort"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

const (
	permissionAllowOnce  = "allow-once"
	permissionRejectOnce = "reject-once"
)

type interactionPhase uint8

const (
	interactionPending interactionPhase = iota
	interactionAnswering
	interactionAnswered
)

type pendingInteraction struct {
	ref             agent.InteractionRef
	kind            agent.InteractionKind
	requestID       string
	request         claude.IncomingRequest
	received        claude.Received
	questions       map[string]struct{}
	phase           interactionPhase
	answerOutcome   agent.InteractionOutcome
	answerAccepted  bool
	terminalOutcome agent.InteractionOutcome
	terminalReason  string
	answerDone      chan struct{}
	sequence        uint64
	startedAt       time.Time
}

func (s *session) convertRequest(received claude.Received) ([]agent.Event, error) {
	if received.Request == nil {
		return nil, nil
	}
	request := *received.Request
	if request.ID() == "" {
		return s.rejectUnsupportedRequest(
			received,
			"native_request_identity_missing",
			"Claude control request has no identity",
			"Permission request has no identity",
		)
	}
	if request.Method() != "can_use_tool" {
		return s.rejectUnsupportedRequest(
			received,
			"native_request_unsupported",
			"Claude control request is not supported by this adapter",
			"Unsupported by the unified agent adapter",
		)
	}
	handle := request.Handle()
	if handle == nil {
		return s.gap(
			received,
			"native_request_invalid",
			"Claude permission request has no native answer handle",
			map[string]any{"requestId": request.ID()},
		)
	}
	metadata, err := handle.Metadata()
	if err != nil {
		return s.rejectUnsupportedRequest(
			received,
			"native_request_invalid",
			"Claude permission request cannot be decoded",
			"Invalid Claude permission request",
		)
	}
	if metadata.ToolUseID == "" {
		return s.rejectUnsupportedRequest(
			received,
			"permission_tool_identity_missing",
			"Claude permission request has no tool-use identity",
			"Permission request has no tool-use identity",
		)
	}
	s.mu.Lock()
	attributed := !s.closed && (s.submitting || s.workActive)
	s.mu.Unlock()
	if !attributed {
		return s.rejectUnsupportedRequest(
			received,
			"interaction_attribution_unknown",
			"Claude interaction cannot be attributed to current session work",
			"Permission request is not attributable to current session work",
		)
	}
	if metadata.ToolName == "AskUserQuestion" {
		return s.questionInteraction(received, request, metadata)
	}
	return s.permissionInteraction(received, request, metadata)
}

func (s *session) permissionInteraction(
	received claude.Received,
	request claude.IncomingRequest,
	metadata claude.PermissionMetadata,
) ([]agent.Event, error) {
	title := metadata.Title
	if title == "" {
		title = metadata.DisplayName
	}
	if title == "" {
		title = "Approve " + metadata.ToolName
	}
	interaction := &pendingInteraction{
		kind:      agent.InteractionPermission,
		requestID: request.ID(),
		request:   request,
		received:  received,
		phase:     interactionPending,
		startedAt: nativeEventTime(request.Raw()),
	}
	unified := agent.InteractionRequest{
		Kind:       agent.InteractionPermission,
		ToolCallID: metadata.ToolUseID,
		Title:      title,
		Content: []agent.ContentBlock{
			agent.TextBlock{Text: string(request.Params())},
		},
		Options: []agent.PermissionOption{
			{
				OptionID: permissionAllowOnce,
				Name:     "Allow once",
				Kind:     agent.PermissionAllowOnce,
			},
			{
				OptionID: permissionRejectOnce,
				Name:     "Reject once",
				Kind:     agent.PermissionRejectOnce,
			},
		},
	}
	return s.registerInteraction(interaction, unified)
}

func (s *session) questionInteraction(
	received claude.Received,
	request claude.IncomingRequest,
	metadata claude.PermissionMetadata,
) ([]agent.Event, error) {
	if !s.features.AgentQuestions {
		return s.rejectUnsupportedRequest(
			received,
			"agent_questions_disabled",
			"Claude questions are disabled for this session",
			"AskUserQuestion is disabled for this session",
		)
	}
	var input claude.AskUserQuestionInput
	if err := json.Unmarshal(metadata.Input, &input); err != nil || len(input.Questions) == 0 {
		return s.rejectUnsupportedRequest(
			received,
			"questions_input_invalid",
			"Claude AskUserQuestion input has no valid questions",
			"Invalid AskUserQuestion input",
		)
	}
	schema, err := decodeJSON(metadata.Input)
	if err != nil {
		return s.rejectUnsupportedRequest(
			received,
			"questions_input_invalid",
			"Claude AskUserQuestion input cannot be decoded",
			"Invalid AskUserQuestion input",
		)
	}
	object, ok := schema.(map[string]any)
	if !ok {
		return s.rejectUnsupportedRequest(
			received,
			"questions_input_invalid",
			"Claude AskUserQuestion input is not an object",
			"Invalid AskUserQuestion input",
		)
	}
	questions := make(map[string]struct{}, len(input.Questions))
	for _, question := range input.Questions {
		questions[question.Question] = struct{}{}
	}
	interaction := &pendingInteraction{
		kind:      agent.InteractionForm,
		requestID: request.ID(),
		request:   request,
		received:  received,
		questions: questions,
		phase:     interactionPending,
		startedAt: nativeEventTime(request.Raw()),
	}
	unified := agent.InteractionRequest{
		Kind:            agent.InteractionForm,
		Message:         "Answer native Claude questions",
		RequestedSchema: object,
	}
	return s.registerInteraction(interaction, unified)
}

func (s *session) rejectUnsupportedRequest(
	received claude.Received,
	reason string,
	message string,
	denial string,
) ([]agent.Event, error) {
	rejected := false
	if received.Request != nil {
		ctx := received.Request.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		rejected = received.Request.Deny(ctx, claude.DenialOptions{Message: denial}) == nil
	}
	event, err := s.newEvent(
		received,
		"",
		agent.SessionErrorPayload{Message: message, Reason: reason},
		time.Now().UTC(),
		map[string]any{"requestRejected": rejected},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) registerInteraction(
	interaction *pendingInteraction,
	request agent.InteractionRequest,
) ([]agent.Event, error) {
	ref, err := uuidv4.New()
	if err != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			"interaction_ref_failed",
			"create interaction reference",
			err,
		)
	}
	interaction.ref = agent.InteractionRef("claude-" + ref)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil
	}
	if s.interactions == nil {
		s.interactions = make(map[agent.InteractionRef]*pendingInteraction)
	}
	if s.interactionRefs == nil {
		s.interactionRefs = make(map[string]agent.InteractionRef)
	}
	if existing := s.interactionRefs[interaction.requestID]; existing != "" {
		current := s.interactions[existing]
		s.mu.Unlock()
		return s.gap(
			interaction.received,
			"duplicate_native_request",
			"Claude repeated a permission request identifier",
			map[string]any{
				"requestId":    interaction.requestID,
				"previousLive": current != nil,
			},
		)
	}
	var stateEvent *agent.Event
	if s.state == nil || *s.state != agent.StateRequiresAction {
		var previous *agent.SessionState
		if s.state != nil {
			value := *s.state
			previous = &value
		}
		requiresAction := agent.StateRequiresAction
		event, stateErr := s.newEvent(
			interaction.received,
			"",
			agent.StateUpdatePayload{
				PreviousState: previous,
				State:         &requiresAction,
			},
			interaction.startedAt,
			map[string]any{"nativeState": string(agent.StateRequiresAction)},
		)
		if stateErr != nil {
			s.mu.Unlock()
			return nil, stateErr
		}
		stateEvent = &event
		s.state = &requiresAction
		s.stateVersion++
	}
	s.workActive = true
	requestedEvent, requestErr := s.newEvent(
		interaction.received,
		"",
		agent.InteractionRequestedPayload{
			Interaction: interaction.ref,
			Request:     request,
			Blocking:    true,
		},
		interaction.startedAt,
		nil,
	)
	if requestErr != nil {
		s.mu.Unlock()
		return nil, requestErr
	}
	interaction.sequence = s.nextInteractionSequenceLocked()
	s.interactions[interaction.ref] = interaction
	s.interactionRefs[interaction.requestID] = interaction.ref
	s.mu.Unlock()
	if stateEvent == nil {
		return []agent.Event{requestedEvent}, nil
	}
	return []agent.Event{*stateEvent, requestedEvent}, nil
}

func (s *session) nextInteractionSequenceLocked() uint64 {
	s.interactionSequence++
	return s.interactionSequence
}

func (s *session) Answer(
	ctx context.Context,
	ref agent.InteractionRef,
	answer agent.Answer,
) (agent.InteractionReceipt, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, adapterError(
			agent.ErrorClosed,
			"session_closed",
			"session is closed",
			nil,
		)
	}
	interaction := s.interactions[ref]
	if interaction == nil {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_interaction_ref",
			"interaction reference is not live in this session",
			nil,
		)
	}
	if interaction.kind == agent.InteractionForm && !s.features.AgentQuestions {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, adapterError(
			agent.ErrorUnsupported,
			"agent_questions_disabled",
			"Claude questions are disabled for this session",
			nil,
		)
	}
	if interaction.phase != interactionPending {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, adapterError(
			agent.ErrorFailedPrecondition,
			"interaction_not_pending",
			"interaction is no longer pending",
			nil,
		)
	}
	if cause := context.Cause(interaction.request.Context()); cause != nil {
		outcome, reason := answerTerminalOutcome(interaction, cause)
		interaction.terminalOutcome = outcome
		interaction.terminalReason = reason
		s.deleteInteractionLocked(interaction)
		s.mu.Unlock()
		events, eventErr := s.closedInteractionEvents(
			[]*pendingInteraction{interaction},
			agent.InteractionCancelled,
			reason,
			reason,
			time.Now().UTC(),
		)
		if eventErr != nil {
			return agent.InteractionReceipt{}, eventErr
		}
		if pushErr := s.pushEvents(events); pushErr != nil {
			return agent.InteractionReceipt{}, adapterError(
				agent.ErrorUnavailable,
				"event_delivery_failed",
				"deliver Claude interaction closure",
				pushErr,
			)
		}
		sent := false
		return agent.InteractionReceipt{
				Interaction: ref,
				Acceptance:  agent.Acceptance{Sent: &sent},
			},
			interactionAnswerError(reason, cause)
	}
	if err := agent.ValidateAnswer(interaction.kind, answer); err != nil {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, err
	}
	reply, outcome, validationErr := interactionReply(interaction, answer)
	if validationErr != nil {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, validationErr
	}
	done := make(chan struct{})
	interaction.answerDone = done
	interaction.phase = interactionAnswering
	s.mu.Unlock()

	answerErr := reply(ctx)

	s.mu.Lock()
	owned := s.interactions[ref] == interaction
	if answerErr == nil {
		interaction.phase = interactionAnswered
		interaction.answerOutcome = outcome
		interaction.answerAccepted = true
		s.mu.Unlock()
		close(done)
		sent := true
		return agent.InteractionReceipt{
			Interaction: ref,
			Acceptance:  agent.Acceptance{Sent: &sent},
		}, nil
	}
	if answerNotSent(answerErr) {
		if context.Cause(interaction.request.Context()) == nil {
			if owned {
				interaction.phase = interactionPending
			}
			s.mu.Unlock()
			close(done)
			sent := false
			return agent.InteractionReceipt{
					Interaction: ref,
					Acceptance:  agent.Acceptance{Sent: &sent},
				},
				nativeOperationError("answer", answerErr)
		}
	}
	outcome, reason := answerTerminalOutcome(
		interaction,
		answerErr,
	)
	interaction.terminalOutcome = outcome
	interaction.terminalReason = reason
	if owned {
		s.deleteInteractionLocked(interaction)
	}
	s.mu.Unlock()
	close(done)

	if owned {
		settled, settleErr := s.closedInteractionEvents(
			[]*pendingInteraction{interaction},
			agent.InteractionCancelled,
			reason,
			reason,
			time.Now().UTC(),
		)
		if settleErr != nil {
			return agent.InteractionReceipt{}, settleErr
		}
		if pushErr := s.pushEvents(settled); pushErr != nil {
			return agent.InteractionReceipt{}, adapterError(
				agent.ErrorUnavailable,
				"event_delivery_failed",
				"deliver Claude interaction closure",
				pushErr,
			)
		}
	}
	sent := false
	return agent.InteractionReceipt{
			Interaction: ref,
			Acceptance:  agent.Acceptance{Sent: &sent},
		},
		interactionAnswerError(reason, answerErr)
}

func answerTerminalOutcome(
	interaction *pendingInteraction,
	err error,
) (agent.InteractionOutcome, string) {
	cause := context.Cause(interaction.request.Context())
	switch {
	case errors.Is(cause, claude.ErrUnknownOutcome),
		errors.Is(err, claude.ErrUnknownOutcome):
		return agent.InteractionFailed, "answer_outcome_unknown"
	case errors.Is(cause, claude.ErrPermissionUsed),
		errors.Is(cause, claude.ErrPermissionStale),
		errors.Is(err, claude.ErrPermissionUsed),
		errors.Is(err, claude.ErrPermissionStale):
		return agent.InteractionExpired, "native_request_ended"
	case errors.Is(cause, claude.ErrPermissionCancelled):
		return agent.InteractionCancelled, "native_request_ended"
	case errors.Is(err, claude.ErrPermissionCancelled):
		return agent.InteractionCancelled, "native_request_ended"
	case errors.Is(cause, claude.ErrClosed),
		errors.Is(err, claude.ErrClosed):
		return agent.InteractionCancelled, "session_closed"
	default:
		return agent.InteractionFailed, answerFailureReason(err)
	}
}

func interactionReply(
	interaction *pendingInteraction,
	answer agent.Answer,
) (func(context.Context) error, agent.InteractionOutcome, error) {
	concrete, err := concreteAnswer(answer)
	if err != nil {
		return nil, "", err
	}
	switch value := concrete.(type) {
	case agent.PermissionAnswer:
		if value.Outcome == agent.PermissionCancelled {
			return func(ctx context.Context) error {
				return interaction.request.Deny(ctx, claude.DenialOptions{
					Message: "User canceled the request",
				})
			}, agent.InteractionCancelled, nil
		}
		switch value.OptionID {
		case permissionAllowOnce:
			return func(ctx context.Context) error {
				return interaction.request.Approve(ctx, claude.ApprovalOptions{})
			}, agent.InteractionAnswered, nil
		case permissionRejectOnce:
			return func(ctx context.Context) error {
				return interaction.request.Deny(ctx, claude.DenialOptions{
					Message: "User denied the request",
				})
			}, agent.InteractionAnswered, nil
		default:
			return nil, "", adapterError(
				agent.ErrorInvalidArgument,
				"permission_option_not_offered",
				"permission option was not offered by the native request",
				nil,
			)
		}
	case agent.FormAnswer:
		switch value.Outcome {
		case agent.FormAccept:
			answers, answerErr := formAnswers(value.Content, interaction.questions)
			if answerErr != nil {
				return nil, "", answerErr
			}
			return func(ctx context.Context) error {
				return interaction.request.Answer(
					ctx,
					claude.QuestionAnswers{Answers: answers},
				)
			}, agent.InteractionAnswered, nil
		case agent.FormDecline:
			return func(ctx context.Context) error {
				return interaction.request.Deny(ctx, claude.DenialOptions{
					Message: "User declined the questions",
				})
			}, agent.InteractionCompleted, nil
		case agent.FormCancel:
			return func(ctx context.Context) error {
				return interaction.request.Deny(ctx, claude.DenialOptions{
					Message: "User canceled the questions",
				})
			}, agent.InteractionCancelled, nil
		default:
			return nil, "", adapterError(
				agent.ErrorInvalidArgument,
				"invalid_form_outcome",
				"form answer outcome is not supported",
				nil,
			)
		}
	default:
		return nil, "", adapterError(
			agent.ErrorUnsupported,
			"answer_kind_unsupported",
			"answer kind is not supported for this Claude interaction",
			nil,
		)
	}
}

func formAnswers(
	content map[string]any,
	questions map[string]struct{},
) (map[string]string, error) {
	raw, present := content["answers"]
	if len(content) != 1 || !present {
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer must contain only a native answers object",
			nil,
		)
	}
	answers := make(map[string]string)
	switch value := raw.(type) {
	case map[string]string:
		maps.Copy(answers, value)
	case map[string]any:
		for key, item := range value {
			answer, ok := item.(string)
			if !ok {
				switch item.(type) {
				case []any, []string:
					return nil, adapterError(
						agent.ErrorUnsupported,
						"multi_select_form_answers_unsupported",
						"Claude multi-select question answers are not supported",
						nil,
					)
				}
				return nil, adapterError(
					agent.ErrorInvalidArgument,
					"invalid_form_answer",
					"form answers must be strings",
					nil,
				)
			}
			answers[key] = answer
		}
	default:
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer does not contain a native answers object",
			nil,
		)
	}
	for key := range answers {
		if _, known := questions[key]; !known {
			return nil, adapterError(
				agent.ErrorInvalidArgument,
				"unknown_form_question",
				"form answer uses a question not present in the native request",
				nil,
			)
		}
	}
	return answers, nil
}

func concreteAnswer(answer agent.Answer) (agent.Answer, error) {
	if answer == nil {
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"missing_answer",
			"answer is required",
			nil,
		)
	}
	value := reflect.ValueOf(answer)
	if value.Kind() != reflect.Pointer {
		return answer, nil
	}
	if value.IsNil() {
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"missing_answer",
			"answer is required",
			nil,
		)
	}
	concrete, ok := value.Elem().Interface().(agent.Answer)
	if !ok {
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"unsupported_answer",
			"answer kind is not supported",
			nil,
		)
	}
	return concrete, nil
}

func (s *session) deleteInteractionLocked(interaction *pendingInteraction) {
	delete(s.interactions, interaction.ref)
	if s.interactionRefs[interaction.requestID] == interaction.ref {
		delete(s.interactionRefs, interaction.requestID)
	}
}

func (s *session) interactionClosedEvent(
	received claude.Received,
	interaction *pendingInteraction,
	outcome agent.InteractionOutcome,
	reason string,
	at time.Time,
) (agent.Event, error) {
	return s.newEvent(
		received,
		"",
		agent.InteractionClosedPayload{
			Interaction: interaction.ref,
			Outcome:     outcome,
			Reason:      reason,
		},
		at,
		map[string]any{"requestId": interaction.requestID},
	)
}

func (s *session) takeAllInteractionsLocked() []*pendingInteraction {
	interactions := make([]*pendingInteraction, 0, len(s.interactions))
	for _, interaction := range s.interactions {
		interactions = append(interactions, interaction)
	}
	s.interactions = make(map[agent.InteractionRef]*pendingInteraction)
	s.interactionRefs = make(map[string]agent.InteractionRef)
	sort.Slice(interactions, func(left, right int) bool {
		return interactions[left].sequence < interactions[right].sequence
	})
	return interactions
}

func (s *session) settleInteractionsForState(
	state agent.SessionState,
) []*pendingInteraction {
	if state == agent.StateRequiresAction {
		return nil
	}
	s.mu.Lock()
	settled := s.takeAllInteractionsLocked()
	s.mu.Unlock()
	return settled
}

func (s *session) closeInteractionEvents(
	interactions []*pendingInteraction,
	at time.Time,
) ([]agent.Event, error) {
	return s.closedInteractionEvents(
		interactions,
		agent.InteractionCancelled,
		"session_closed",
		"session_closed",
		at,
	)
}

func (s *session) closedInteractionEvents(
	interactions []*pendingInteraction,
	fallbackOutcome agent.InteractionOutcome,
	fallbackReason string,
	answeredReason string,
	at time.Time,
) ([]agent.Event, error) {
	sort.Slice(interactions, func(left, right int) bool {
		return interactions[left].sequence < interactions[right].sequence
	})
	events := make([]agent.Event, 0, len(interactions))
	for _, interaction := range interactions {
		s.waitInteractionAnswer(interaction)
		s.mu.Lock()
		outcome, reason := interactionClosureLocked(
			interaction,
			fallbackOutcome,
			fallbackReason,
			answeredReason,
		)
		s.mu.Unlock()
		event, err := s.interactionClosedEvent(
			interaction.received,
			interaction,
			outcome,
			reason,
			at,
		)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (s *session) waitInteractionAnswer(interaction *pendingInteraction) {
	s.mu.Lock()
	done := interaction.answerDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func interactionClosureLocked(
	interaction *pendingInteraction,
	fallbackOutcome agent.InteractionOutcome,
	fallbackReason string,
	answeredReason string,
) (agent.InteractionOutcome, string) {
	if interaction.terminalReason != "" {
		return interaction.terminalOutcome, interaction.terminalReason
	}
	if interaction.phase == interactionAnswered && interaction.answerAccepted {
		return interaction.answerOutcome, answeredReason
	}
	return fallbackOutcome, fallbackReason
}

func (s *session) cancelInteraction(
	received claude.Received,
) ([]agent.Event, error) {
	cancel := received.Event.Cancel
	if cancel == nil {
		return nil, nil
	}
	s.mu.Lock()
	ref := s.interactionRefs[cancel.RequestID]
	interaction := s.interactions[ref]
	if interaction == nil {
		s.mu.Unlock()
		return s.gap(
			received,
			"interaction_cancel_unknown",
			"Claude canceled an interaction that is not live in this session",
			map[string]any{"requestId": cancel.RequestID},
		)
	}
	s.deleteInteractionLocked(interaction)
	s.mu.Unlock()
	return s.closedInteractionEvents(
		[]*pendingInteraction{interaction},
		agent.InteractionCancelled,
		"native_cancel",
		"native_cancel",
		nativeEventTime(received.Raw),
	)
}

func answerNotSent(err error) bool {
	var dispatch *claude.DispatchError
	if errors.As(err, &dispatch) {
		return dispatch.State == claude.NotSent
	}
	return false
}

func answerFailureReason(err error) string {
	switch {
	case errors.Is(err, claude.ErrUnknownOutcome):
		return "answer_outcome_unknown"
	case errors.Is(err, claude.ErrPermissionCancelled),
		errors.Is(err, claude.ErrPermissionStale),
		errors.Is(err, claude.ErrPermissionUsed):
		return "native_request_ended"
	case errors.Is(err, claude.ErrClosed):
		return "session_closed"
	default:
		return "answer_failed"
	}
}

func interactionAnswerError(reason string, cause error) error {
	switch reason {
	case "session_closed":
		return adapterError(
			agent.ErrorClosed,
			reason,
			"Claude session closed before the interaction could be answered",
			cause,
		)
	case "answer_outcome_unknown":
		return adapterError(
			agent.ErrorOutcomeUnknown,
			reason,
			"Claude interaction answer outcome is unknown; do not retry",
			cause,
		)
	case "native_request_ended":
		return adapterError(
			agent.ErrorFailedPrecondition,
			reason,
			"Claude interaction is no longer answerable",
			cause,
		)
	default:
		return nativeOperationError("answer", cause)
	}
}
