package agentcodex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sort"
	"strconv"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

type interactionPhase uint8

const (
	interactionPending interactionPhase = iota
	interactionAnswering
	interactionAnswered
)

type pendingInteraction struct {
	ref        agent.InteractionRef
	kind       agent.InteractionKind
	method     string
	requestKey string
	startedAt  time.Time
	turnID     string
	itemID     string
	threadID   string
	blocking   bool
	request    codex.IncomingRequest
	received   codex.Received
	choices    map[string]func(context.Context) error
	cancel     func(context.Context) error
	questions  map[string]struct{}
	phase      interactionPhase
	answerDone chan struct{}
	answered   bool
	answerErr  error
	sequence   uint64
}

func (s *session) convertRequest(received codex.Received) ([]agent.Event, error) {
	if received.Request == nil {
		return nil, invalidNativeEvent("")
	}
	request := *received.Request
	threadID := s.interactionThread(received)
	if threadID == "" {
		return s.unsupportedRequest(
			received,
			"foreign_thread_request",
			"Codex requested an interaction outside this session tree",
		)
	}
	if err := request.Handle().Err(); err != nil {
		return s.notificationGap(
			received,
			"native_request_ended",
			"Codex native request ended before it could be exposed",
		)
	}

	switch request.Method() {
	case "item/commandExecution/requestApproval":
		return s.commandInteraction(received, request, threadID)
	case "item/fileChange/requestApproval":
		return s.fileChangeInteraction(received, request, threadID)
	case "item/tool/requestUserInput":
		return s.formInteraction(received, request, threadID)
	case "item/permissions/requestApproval":
		return s.unsupportedRequest(
			received,
			"permission_profile_unsupported",
			"Codex permission-profile approval cannot be represented safely",
		)
	default:
		return s.unsupportedRequest(
			received,
			"native_request_unsupported",
			"Codex native request is not supported by this adapter",
		)
	}
}

func (s *session) commandInteraction(
	received codex.Received,
	request codex.IncomingRequest,
	threadID string,
) ([]agent.Event, error) {
	data, err := request.Data()
	if err != nil {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex command approval cannot be decoded",
		)
	}
	params, ok := data.(*codextypes.CommandExecutionRequestApprovalParams)
	if !ok || params.ItemID == "" || params.TurnID == "" || params.ThreadID != threadID {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex command approval has invalid identity",
		)
	}
	options, choices, supported := commandApprovalChoices(request, params)
	if !supported {
		return s.unsupportedRequest(
			received,
			"permission_option_unsupported",
			"Codex command approval contains unsupported options",
		)
	}
	cancel := commandCancelChoice(request, params)
	interaction := &pendingInteraction{
		kind:      agent.InteractionPermission,
		method:    request.Method(),
		startedAt: unixMilliOrNow(params.StartedAtMs),
		turnID:    params.TurnID,
		itemID:    params.ItemID,
		threadID:  params.ThreadID,
		blocking:  true,
		request:   request,
		received:  received,
		choices:   choices,
		cancel:    cancel,
		phase:     interactionPending,
	}
	unifiedRequest := agent.InteractionRequest{
		Kind:       agent.InteractionPermission,
		ToolCallID: params.ItemID,
		Title:      "Approve command execution",
		Options:    options,
	}
	return s.registerInteraction(interaction, unifiedRequest)
}

func (s *session) fileChangeInteraction(
	received codex.Received,
	request codex.IncomingRequest,
	threadID string,
) ([]agent.Event, error) {
	data, err := request.Data()
	if err != nil {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex file-change approval cannot be decoded",
		)
	}
	params, ok := data.(*codextypes.FileChangeRequestApprovalParams)
	if !ok || params.ItemID == "" || params.TurnID == "" || params.ThreadID != threadID {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex file-change approval has invalid identity",
		)
	}
	options, choices := fileChangeApprovalChoices(request)
	interaction := &pendingInteraction{
		kind:      agent.InteractionPermission,
		method:    request.Method(),
		startedAt: unixMilliOrNow(params.StartedAtMs),
		turnID:    params.TurnID,
		itemID:    params.ItemID,
		threadID:  params.ThreadID,
		blocking:  true,
		request:   request,
		received:  received,
		choices:   choices,
		cancel:    choices["cancel"],
		phase:     interactionPending,
	}
	unifiedRequest := agent.InteractionRequest{
		Kind:       agent.InteractionPermission,
		ToolCallID: params.ItemID,
		Title:      "Approve file changes",
		Options:    options,
	}
	return s.registerInteraction(interaction, unifiedRequest)
}

func (s *session) formInteraction(
	received codex.Received,
	request codex.IncomingRequest,
	threadID string,
) ([]agent.Event, error) {
	if !s.features.AgentQuestions {
		return s.unsupportedRequest(
			received,
			"agent_questions_disabled",
			"Codex user-input requests are disabled for this session",
		)
	}
	data, err := request.Data()
	if err != nil {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex user-input request cannot be decoded",
		)
	}
	params, ok := data.(*codextypes.ToolRequestUserInputParams)
	if !ok || params.ItemID == "" || params.TurnID == "" || params.ThreadID != threadID {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex user-input request has invalid identity",
		)
	}
	questions := make(map[string]struct{}, len(params.Questions))
	for _, question := range params.Questions {
		if question.ID == "" {
			return s.unsupportedRequest(
				received,
				"invalid_native_request",
				"Codex user-input request contains an empty question ID",
			)
		}
		if _, duplicate := questions[question.ID]; duplicate {
			return s.unsupportedRequest(
				received,
				"invalid_native_request",
				"Codex user-input request contains duplicate question IDs",
			)
		}
		questions[question.ID] = struct{}{}
	}
	if len(questions) == 0 {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex user-input request contains no questions",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Params()))
	decoder.UseNumber()
	var nativeSchema map[string]any
	if err := decoder.Decode(&nativeSchema); err != nil {
		return s.unsupportedRequest(
			received,
			"invalid_native_request",
			"Codex user-input request cannot be represented",
		)
	}
	interaction := &pendingInteraction{
		kind:      agent.InteractionForm,
		method:    request.Method(),
		startedAt: time.Now().UTC(),
		turnID:    params.TurnID,
		itemID:    params.ItemID,
		threadID:  params.ThreadID,
		blocking:  params.IsBlocking,
		request:   request,
		received:  received,
		questions: questions,
		phase:     interactionPending,
	}
	unifiedRequest := agent.InteractionRequest{
		Kind:            agent.InteractionForm,
		Message:         "Answer native Codex questions",
		RequestedSchema: formRequestedSchema(nativeSchema, params),
	}
	return s.registerInteraction(interaction, unifiedRequest)
}

func (s *session) interactionThread(received codex.Received) string {
	return confirmedInteractionThread(
		s.ID(),
		received.Source.ThreadID,
		s.parentThread,
	)
}

func confirmedInteractionThread(
	sessionID string,
	threadID string,
	parentThread func(string) string,
) string {
	if threadID == "" {
		return ""
	}
	if threadID == sessionID {
		return threadID
	}
	seen := make(map[string]struct{})
	for current := threadID; current != ""; {
		if _, duplicate := seen[current]; duplicate {
			return ""
		}
		seen[current] = struct{}{}
		parent := parentThread(current)
		if parent == sessionID {
			return threadID
		}
		current = parent
	}
	return ""
}

func formRequestedSchema(
	nativeSchema map[string]any,
	params *codextypes.ToolRequestUserInputParams,
) map[string]any {
	answers := make(map[string]any, len(params.Questions))
	for _, question := range params.Questions {
		answers[question.ID] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answers": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
			},
			"required":             []any{"answers"},
			"additionalProperties": false,
		}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answers": map[string]any{
				"type":                 "object",
				"properties":           answers,
				"additionalProperties": false,
			},
		},
		"required":             []any{"answers"},
		"additionalProperties": false,
	}
	if questions, present := nativeSchema["questions"]; present {
		schema["x-codex-questions"] = questions
	}
	return schema
}

func (s *session) registerInteraction(
	interaction *pendingInteraction,
	request agent.InteractionRequest,
) ([]agent.Event, error) {
	ref, err := newInteractionRef()
	if err != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			"interaction_ref_failed",
			"create interaction reference",
			err,
		)
	}
	interaction.ref = ref
	interaction.requestKey = requestIDKey(interaction.request.ID())

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil
	}
	if existing := s.requestRefs[interaction.requestKey]; existing != "" {
		previous := s.interactions[existing]
		s.mu.Unlock()
		if previous != nil && previous.phase == interactionAnswered {
			return s.unsupportedRequest(
				interaction.received,
				"duplicate_native_request",
				"Codex reused an interaction request ID before native settlement",
			)
		}
		return s.notificationGap(
			interaction.received,
			"duplicate_native_request",
			"Codex repeated an interaction request; the original reference remains authoritative",
		)
	}
	if interaction.threadID == s.ID() {
		if s.activeTurn == "" {
			if (!s.submitting && !s.submitUnknown) ||
				s.lastFinished == interaction.turnID {
				s.mu.Unlock()
				return s.unsupportedRequest(
					interaction.received,
					"stale_native_request",
					"Codex native request does not belong to the current session work",
				)
			}
			s.activeTurn = interaction.turnID
			s.noteWorkEvidenceLocked()
		}
		if s.activeTurn != interaction.turnID || s.lastFinished == interaction.turnID {
			s.mu.Unlock()
			return s.unsupportedRequest(
				interaction.received,
				"stale_native_request",
				"Codex native request does not belong to the current session work",
			)
		}
	}

	var stateEvent *agent.Event
	if interaction.blocking &&
		(interaction.threadID == s.ID() || s.activeTurn != "") {
		var previous *agent.SessionState
		if s.state != nil {
			value := *s.state
			previous = &value
		}
		requiresAction := agent.StateRequiresAction
		event, stateErr := s.newEvent(
			interaction.received,
			interaction.method,
			"",
			agent.StateUpdatePayload{
				PreviousState: previous,
				State:         &requiresAction,
			},
			interaction.startedAt,
			nil,
		)
		if stateErr != nil {
			s.mu.Unlock()
			return nil, stateErr
		}
		stateEvent = &event
		s.state = &requiresAction
	}
	requestedEvent, requestErr := s.newEvent(
		interaction.received,
		interaction.method,
		"",
		agent.InteractionRequestedPayload{
			Interaction: interaction.ref,
			Request:     request,
			Blocking:    interaction.blocking,
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
	if interaction.requestKey != "" {
		s.requestRefs[interaction.requestKey] = interaction.ref
	}
	s.mu.Unlock()
	if stateEvent == nil {
		return []agent.Event{requestedEvent}, nil
	}
	return []agent.Event{*stateEvent, requestedEvent}, nil
}

func (s *session) unsupportedRequest(
	received codex.Received,
	reason string,
	message string,
) ([]agent.Event, error) {
	request := received.Request
	rejected := false
	if request != nil {
		ctx := request.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		rejected = request.Handle().Reject(ctx, &codex.RPCError{
			Code:    -32602,
			Message: "Unsupported by the unified agent adapter",
		}) == nil
	}
	event, err := s.sessionErrorEvent(
		received,
		requestMethod(received),
		reason,
		message,
		map[string]any{"requestRejected": rejected},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) answerInteraction(
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
			"Codex user-input requests are disabled for this session",
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
	if err := interaction.request.Handle().Err(); err != nil {
		return s.finishTerminalInteractionAndUnlock(interaction, err)
	}
	matched, err := agent.MatchAnswer(interaction.kind, answer)
	if err != nil {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, err
	}
	reply, validationErr := interactionReply(interaction, matched)
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
	interaction.answered = answerErr == nil
	interaction.answerErr = answerErr
	if interaction.answered {
		interaction.phase = interactionAnswered
	}
	close(interaction.answerDone)
	interaction.answerDone = nil
	current := s.interactions[ref]
	if current != interaction {
		s.mu.Unlock()
		if answerErr != nil {
			return agent.InteractionReceipt{Interaction: ref}, nativeOperationError("answer", answerErr)
		}
		sent := true
		return agent.InteractionReceipt{
			Interaction: ref,
			Acceptance:  agent.Acceptance{Sent: &sent},
		}, nil
	}

	if answerErr != nil {
		if answerOutcomeUnknown(answerErr) {
			s.removeInteractionLocked(interaction)
			statePayload := s.interactionsSettledLocked()
			s.mu.Unlock()
			s.pushInteractionSettlement(
				interaction,
				agent.InteractionFailed,
				"answer_outcome_unknown",
				statePayload,
			)
			return agent.InteractionReceipt{Interaction: ref}, nativeOperationError("answer", answerErr)
		}
		if interactionStillLive(interaction) {
			interaction.phase = interactionPending
			interaction.answerErr = nil
			s.mu.Unlock()
			sent := false
			return agent.InteractionReceipt{
				Interaction: ref,
				Acceptance:  agent.Acceptance{Sent: &sent},
			}, nativeOperationError("answer", answerErr)
		}
		outcome, reason := terminalInteractionOutcome(answerErr)
		if outcome == "" {
			outcome, reason = agent.InteractionExpired, "native_request_ended"
		}
		s.removeInteractionLocked(interaction)
		statePayload := s.interactionsSettledLocked()
		s.mu.Unlock()
		s.pushInteractionSettlement(interaction, outcome, reason, statePayload)
		sent := false
		return agent.InteractionReceipt{
			Interaction: ref,
			Acceptance:  agent.Acceptance{Sent: &sent},
		}, nativeOperationError("answer", answerErr)
	}

	s.mu.Unlock()
	sent := true
	return agent.InteractionReceipt{
		Interaction: ref,
		Acceptance:  agent.Acceptance{Sent: &sent},
	}, nil
}

// finishTerminalInteractionAndUnlock requires s.mu held and releases it.
func (s *session) finishTerminalInteractionAndUnlock(
	interaction *pendingInteraction,
	cause error,
) (agent.InteractionReceipt, error) {
	outcome, reason := terminalInteractionOutcome(cause)
	if outcome == "" {
		s.mu.Unlock()
		return agent.InteractionReceipt{}, nativeOperationError("answer", cause)
	}
	s.removeInteractionLocked(interaction)
	statePayload := s.interactionsSettledLocked()
	s.mu.Unlock()
	s.pushInteractionSettlement(interaction, outcome, reason, statePayload)
	sent := false
	return agent.InteractionReceipt{
		Interaction: interaction.ref,
		Acceptance:  agent.Acceptance{Sent: &sent},
	}, nativeOperationError("answer", cause)
}

func interactionReply(
	interaction *pendingInteraction,
	answer agent.Answer,
) (func(context.Context) error, error) {
	switch concrete := answer.(type) {
	case agent.PermissionAnswer:
		if concrete.Outcome == agent.PermissionCancelled {
			if interaction.cancel == nil {
				return nil, adapterError(
					agent.ErrorUnsupported,
					"permission_cancel_unsupported",
					"this Codex permission request has no proven cancellation",
					nil,
				)
			}
			return interaction.cancel, nil
		}
		reply := interaction.choices[concrete.OptionID]
		if reply == nil {
			return nil, adapterError(
				agent.ErrorInvalidArgument,
				"permission_option_not_offered",
				"permission option was not offered by the native request",
				nil,
			)
		}
		return reply, nil
	case agent.FormAnswer:
		if concrete.Outcome != agent.FormAccept {
			return nil, adapterError(
				agent.ErrorUnsupported,
				"form_outcome_unsupported",
				"Codex user-input decline and cancellation are not proven",
				nil,
			)
		}
		response, err := formResponse(interaction, concrete.Content)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) error {
			return interaction.request.Answer(ctx, response)
		}, nil
	default:
		return nil, adapterError(
			agent.ErrorUnsupported,
			"answer_kind_unsupported",
			"answer kind is not supported for this Codex interaction",
			nil,
		)
	}
}

func formResponse(
	interaction *pendingInteraction,
	content map[string]any,
) (codextypes.ToolRequestUserInputResponse, error) {
	var response codextypes.ToolRequestUserInputResponse
	if _, present := content["answers"]; len(content) != 1 || !present {
		return response, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer must contain only a native answers object",
			nil,
		)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return response, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer cannot be encoded",
			err,
		)
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Answers == nil {
		return codextypes.ToolRequestUserInputResponse{}, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer does not match the native response shape",
			err,
		)
	}
	if len(response.UnknownFields) != 0 {
		return codextypes.ToolRequestUserInputResponse{}, adapterError(
			agent.ErrorInvalidArgument,
			"invalid_form_answer",
			"form answer contains unsupported response fields",
			nil,
		)
	}
	for questionID, answer := range response.Answers {
		if _, known := interaction.questions[questionID]; !known {
			return codextypes.ToolRequestUserInputResponse{}, adapterError(
				agent.ErrorInvalidArgument,
				"unknown_form_question",
				"form answer uses a question ID not present in the native request",
				nil,
			)
		}
		if len(answer.UnknownFields) != 0 {
			return codextypes.ToolRequestUserInputResponse{}, adapterError(
				agent.ErrorInvalidArgument,
				"invalid_form_answer",
				"form answer contains unsupported question fields",
				nil,
			)
		}
		if answer.Answers == nil {
			return codextypes.ToolRequestUserInputResponse{}, adapterError(
				agent.ErrorInvalidArgument,
				"invalid_form_answer",
				"form answers must be string arrays",
				nil,
			)
		}
	}
	return response, nil
}

func (s *session) resolveInteraction(received codex.Received) ([]agent.Event, error) {
	event := received.Event
	if event == nil {
		return nil, nil
	}
	data, ok := event.Data.(*codextypes.ServerRequestResolvedNotification)
	if !ok {
		return nil, invalidNativeEvent(event.Method)
	}
	key := resolvedRequestIDKey(data.RequestID)
	if key == "" {
		return nil, nil
	}

	s.mu.Lock()
	ref := s.requestRefs[key]
	interaction := s.interactions[ref]
	if interaction == nil || interaction.threadID != data.ThreadID {
		s.mu.Unlock()
		return nil, nil
	}
	s.removeInteractionLocked(interaction)
	statePayload := s.interactionsSettledLocked()
	s.mu.Unlock()

	at := time.Now().UTC()
	outcome, reason := s.settlement(
		interaction,
		agent.InteractionCancelled,
		"native_resolved",
	)
	return s.interactionSettlementEvents(
		received,
		interaction,
		outcome,
		reason,
		at,
		statePayload,
	)
}

func (s *session) closedInteractionEvents(
	interactions []*pendingInteraction,
	outcome agent.InteractionOutcome,
	reason string,
	at time.Time,
) []agent.Event {
	events := make([]agent.Event, 0, len(interactions))
	for _, interaction := range interactions {
		itemOutcome, itemReason := s.settlement(interaction, outcome, reason)
		settled, err := s.interactionSettlementEvents(
			interaction.received,
			interaction,
			itemOutcome,
			itemReason,
			at,
			nil,
		)
		if err == nil {
			events = append(events, settled...)
		}
	}
	return events
}

func (s *session) interactionSettlementEvents(
	received codex.Received,
	interaction *pendingInteraction,
	outcome agent.InteractionOutcome,
	reason string,
	at time.Time,
	statePayload *agent.StateUpdatePayload,
) ([]agent.Event, error) {
	closed, err := s.interactionClosedEvent(
		received,
		interaction,
		outcome,
		reason,
		at,
		nil,
	)
	if err != nil || statePayload == nil {
		return []agent.Event{closed}, err
	}
	stateEvent, err := s.stateUpdateEvent(
		received,
		*statePayload,
		at,
		interaction.turnID,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{closed, stateEvent}, nil
}

func (s *session) interactionClosedEvent(
	received codex.Received,
	interaction *pendingInteraction,
	outcome agent.InteractionOutcome,
	reason string,
	at time.Time,
	extras map[string]any,
) (agent.Event, error) {
	meta := interactionCloseMeta(interaction)
	maps.Copy(meta, extras)
	return s.newEvent(
		received,
		interaction.method,
		"",
		agent.InteractionClosedPayload{
			Interaction: interaction.ref,
			Outcome:     outcome,
			Reason:      reason,
		},
		at,
		meta,
	)
}

func (s *session) stateUpdateEvent(
	received codex.Received,
	payload agent.StateUpdatePayload,
	at time.Time,
	turnID string,
) (agent.Event, error) {
	extras := map[string]any(nil)
	if turnID != "" {
		extras = map[string]any{"turnId": turnID}
	}
	return s.newEvent(received, requestMethod(received), "", payload, at, extras)
}

func (s *session) pushInteractionSettlement(
	interaction *pendingInteraction,
	outcome agent.InteractionOutcome,
	reason string,
	statePayload *agent.StateUpdatePayload,
) {
	at := time.Now().UTC()
	events, err := s.interactionSettlementEvents(
		interaction.received,
		interaction,
		outcome,
		reason,
		at,
		statePayload,
	)
	if err != nil {
		return
	}
	_ = s.pushEvents(events)
}

func (s *session) settlement(
	interaction *pendingInteraction,
	fallback agent.InteractionOutcome,
	fallbackReason string,
) (agent.InteractionOutcome, string) {
	s.mu.Lock()
	done := interaction.answerDone
	s.mu.Unlock()
	if done != nil {
		<-done
	}

	s.mu.Lock()
	answered := interaction.answered
	answerErr := interaction.answerErr
	s.mu.Unlock()
	if answered {
		return agent.InteractionAnswered, fallbackReason
	}
	if answerOutcomeUnknown(answerErr) {
		return agent.InteractionFailed, "answer_outcome_unknown"
	}
	return fallback, fallbackReason
}

func interactionCloseMeta(interaction *pendingInteraction) map[string]any {
	meta := map[string]any{
		"request": interaction.request.Raw(),
	}
	if encoded, err := json.Marshal(interaction.request.ID()); err == nil {
		meta["requestId"] = json.RawMessage(encoded)
	}
	return meta
}

func (s *session) interactionsSettledLocked() *agent.StateUpdatePayload {
	if s.state == nil || *s.state != agent.StateRequiresAction ||
		s.blockingInteractionsLocked() != 0 {
		return nil
	}
	previous := *s.state
	next := agent.StateIdle
	if s.activeTurn != "" {
		next = agent.StateRunning
	}
	s.state = &next
	return &agent.StateUpdatePayload{
		PreviousState: &previous,
		State:         &next,
	}
}

func (s *session) blockingInteractionsLocked() int {
	count := 0
	for _, interaction := range s.interactions {
		if interaction.blocking {
			count++
		}
	}
	return count
}

func (s *session) nextInteractionSequenceLocked() uint64 {
	var largestSequence uint64
	for _, interaction := range s.interactions {
		if interaction.sequence > largestSequence {
			largestSequence = interaction.sequence
		}
	}
	return largestSequence + 1
}

func (s *session) removeInteractionLocked(interaction *pendingInteraction) {
	delete(s.interactions, interaction.ref)
	if s.requestRefs[interaction.requestKey] == interaction.ref {
		delete(s.requestRefs, interaction.requestKey)
	}
}

func (s *session) clearInteractionsLocked() []*pendingInteraction {
	interactions := sortedInteractions(s.interactions)
	s.interactions = make(map[agent.InteractionRef]*pendingInteraction)
	s.requestRefs = make(map[string]agent.InteractionRef)
	return interactions
}

func (s *session) takeTurnInteractionsLocked(turnID string) []*pendingInteraction {
	selected := make(map[agent.InteractionRef]*pendingInteraction)
	for ref, interaction := range s.interactions {
		if interaction.turnID != turnID {
			continue
		}
		selected[ref] = interaction
		delete(s.interactions, ref)
		if s.requestRefs[interaction.requestKey] == ref {
			delete(s.requestRefs, interaction.requestKey)
		}
	}
	return sortedInteractions(selected)
}

func (s *session) takeThreadInteractionsLocked(threadID string) []*pendingInteraction {
	selected := make(map[agent.InteractionRef]*pendingInteraction)
	for ref, interaction := range s.interactions {
		if interaction.threadID != threadID {
			continue
		}
		selected[ref] = interaction
		delete(s.interactions, ref)
		if s.requestRefs[interaction.requestKey] == ref {
			delete(s.requestRefs, interaction.requestKey)
		}
	}
	return sortedInteractions(selected)
}

func (s *session) takeAllInteractionsLocked() []*pendingInteraction {
	interactions := sortedInteractions(s.interactions)
	s.interactions = make(map[agent.InteractionRef]*pendingInteraction)
	s.requestRefs = make(map[string]agent.InteractionRef)
	return interactions
}

func sortedInteractions(
	interactions map[agent.InteractionRef]*pendingInteraction,
) []*pendingInteraction {
	result := make([]*pendingInteraction, 0, len(interactions))
	for _, interaction := range interactions {
		result = append(result, interaction)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].sequence < result[j].sequence
	})
	return result
}

func commandApprovalChoices(
	request codex.IncomingRequest,
	params *codextypes.CommandExecutionRequestApprovalParams,
) ([]agent.PermissionOption, map[string]func(context.Context) error, bool) {
	decisions, present := params.AvailableDecisions.Get()
	if !present {
		decisions = []codextypes.CommandExecutionApprovalDecision{
			commandDecision(codextypes.CommandExecutionApprovalDecisionValueAccept),
			commandDecision(codextypes.CommandExecutionApprovalDecisionValueDecline),
		}
	}
	options := make([]agent.PermissionOption, 0, len(decisions))
	choices := make(map[string]func(context.Context) error, len(decisions))
	seen := make(map[string]struct{}, len(decisions))
	for index, decision := range decisions {
		canonical, err := json.Marshal(decision)
		if err != nil || decision.Unknown != nil {
			return nil, nil, false
		}
		if _, duplicate := seen[string(canonical)]; duplicate {
			return nil, nil, false
		}
		seen[string(canonical)] = struct{}{}

		option := agent.PermissionOption{}
		approve := false
		switch {
		case decision.Value != nil:
			switch *decision.Value {
			case codextypes.CommandExecutionApprovalDecisionValueAccept:
				option = permissionOption("allow-once", "Allow once", agent.PermissionAllowOnce)
				approve = true
			case codextypes.CommandExecutionApprovalDecisionValueAcceptForSession:
				option = permissionOption("allow-session", "Allow for this session", agent.PermissionAllowAlways)
				approve = true
			case codextypes.CommandExecutionApprovalDecisionValueDecline:
				option = permissionOption("reject-once", "Reject once", agent.PermissionRejectOnce)
			case codextypes.CommandExecutionApprovalDecisionValueCancel:
				option = permissionOption("cancel", "Cancel", agent.PermissionRejectOnce)
			default:
				return nil, nil, false
			}
		case decision.AcceptWithExecpolicyAmendment != nil:
			option = permissionOption(
				"native-decision-"+strconv.Itoa(index),
				"Allow and apply the proposed execution policy",
				agent.PermissionAllowAlways,
			)
			approve = true
		case decision.ApplyNetworkPolicyAmendment != nil:
			policy := decision.ApplyNetworkPolicyAmendment.
				ApplyNetworkPolicyAmendment.NetworkPolicyAmendment
			switch policy.Action {
			case codextypes.NetworkPolicyRuleActionAllow:
				option = permissionOption(
					"native-decision-"+strconv.Itoa(index),
					"Allow and apply the proposed network policy",
					agent.PermissionAllowAlways,
				)
				approve = true
			case codextypes.NetworkPolicyRuleActionDeny:
				option = permissionOption(
					"native-decision-"+strconv.Itoa(index),
					"Reject and apply the proposed network policy",
					agent.PermissionRejectAlways,
				)
			default:
				return nil, nil, false
			}
		default:
			return nil, nil, false
		}
		if _, duplicate := choices[option.OptionID]; duplicate {
			return nil, nil, false
		}
		options = append(options, option)
		captured := decision
		capturedApprove := approve
		choices[option.OptionID] = func(ctx context.Context) error {
			response := &codextypes.CommandExecutionRequestApprovalResponse{
				Decision: captured,
			}
			approval := codex.ApprovalOptions{CommandExecution: response}
			if capturedApprove {
				return request.Approve(ctx, approval)
			}
			return request.Deny(ctx, approval)
		}
	}
	return options, choices, len(options) != 0
}

func commandCancelChoice(
	request codex.IncomingRequest,
	params *codextypes.CommandExecutionRequestApprovalParams,
) func(context.Context) error {
	decisions, present := params.AvailableDecisions.Get()
	if !present {
		return nil
	}
	for _, decision := range decisions {
		if decision.Value == nil ||
			*decision.Value != codextypes.CommandExecutionApprovalDecisionValueCancel {
			continue
		}
		captured := decision
		return func(ctx context.Context) error {
			return denyWithCommandDecision(ctx, request, captured)
		}
	}
	return nil
}

func fileChangeApprovalChoices(
	request codex.IncomingRequest,
) ([]agent.PermissionOption, map[string]func(context.Context) error) {
	type choice struct {
		option   agent.PermissionOption
		decision codextypes.FileChangeApprovalDecision
		approve  bool
	}
	items := []choice{
		{permissionOption("allow-once", "Allow once", agent.PermissionAllowOnce), codextypes.FileChangeApprovalDecisionAccept, true},
		{permissionOption("allow-session", "Allow for this session", agent.PermissionAllowAlways), codextypes.FileChangeApprovalDecisionAcceptForSession, true},
		{permissionOption("reject-once", "Reject once", agent.PermissionRejectOnce), codextypes.FileChangeApprovalDecisionDecline, false},
		{permissionOption("cancel", "Cancel", agent.PermissionRejectOnce), codextypes.FileChangeApprovalDecisionCancel, false},
	}
	options := make([]agent.PermissionOption, 0, len(items))
	choices := make(map[string]func(context.Context) error, len(items))
	for _, item := range items {
		options = append(options, item.option)
		response := &codextypes.FileChangeRequestApprovalResponse{Decision: item.decision}
		approval := codex.ApprovalOptions{FileChange: response}
		if item.approve {
			choices[item.option.OptionID] = func(ctx context.Context) error {
				return request.Approve(ctx, approval)
			}
		} else {
			choices[item.option.OptionID] = func(ctx context.Context) error {
				return request.Deny(ctx, approval)
			}
		}
	}
	return options, choices
}

func permissionOption(
	id string,
	name string,
	kind agent.PermissionOptionKind,
) agent.PermissionOption {
	return agent.PermissionOption{OptionID: id, Name: name, Kind: kind}
}

func commandDecision(
	value codextypes.CommandExecutionApprovalDecisionValue,
) codextypes.CommandExecutionApprovalDecision {
	result := codextypes.CommandExecutionApprovalDecision{}
	selected := value
	result.Value = &selected
	return result
}

func denyWithCommandDecision(
	ctx context.Context,
	request codex.IncomingRequest,
	decision codextypes.CommandExecutionApprovalDecision,
) error {
	response := &codextypes.CommandExecutionRequestApprovalResponse{Decision: decision}
	return request.Deny(ctx, codex.ApprovalOptions{CommandExecution: response})
}

func newInteractionRef() (agent.InteractionRef, error) {
	value, err := uuidv4.New()
	if err != nil {
		return "", err
	}
	return agent.InteractionRef("codex-" + value), nil
}

func requestIDKey(id codex.ID) string {
	if value, ok := id.StringValue(); ok {
		return requestKeyForValues(&value, nil)
	}
	if value, ok := id.Integer(); ok {
		return requestKeyForValues(nil, &value)
	}
	return ""
}

func resolvedRequestIDKey(id codextypes.RequestID) string {
	return requestKeyForValues(id.Value, id.Integer)
}

func requestKeyForValues(stringValue *string, integerValue *int64) string {
	if stringValue != nil {
		return "s:" + *stringValue
	}
	if integerValue != nil {
		return "i:" + strconv.FormatInt(*integerValue, 10)
	}
	return ""
}

func interactionStillLive(interaction *pendingInteraction) bool {
	return interaction.request.Handle().Err() == nil &&
		interaction.request.Context().Err() == nil
}

func answerOutcomeUnknown(err error) bool {
	var answerErr *codex.AnswerError
	return errors.As(err, &answerErr) && answerErr.Dispatch == codex.OutcomeUnknown
}

func requestMethod(received codex.Received) string {
	if received.Event != nil {
		return received.Event.Method
	}
	if received.Request != nil {
		return received.Request.Method()
	}
	return ""
}

func terminalInteractionOutcome(err error) (agent.InteractionOutcome, string) {
	switch {
	case errors.Is(err, codex.ErrRequestExpired):
		return agent.InteractionExpired, "native_request_expired"
	case errors.Is(err, codex.ErrRequestCanceled):
		return agent.InteractionCancelled, "native_request_canceled"
	case errors.Is(err, codex.ErrRequestResolved):
		return agent.InteractionCancelled, "native_resolved"
	case errors.Is(err, codex.ErrRequestAnswered):
		return agent.InteractionCancelled, "native_request_answered"
	case errors.Is(err, codex.ErrClosed):
		return agent.InteractionCancelled, "native_request_closed"
	default:
		return "", ""
	}
}
