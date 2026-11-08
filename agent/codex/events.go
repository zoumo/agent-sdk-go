package agentcodex

import (
	"encoding/json"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

func (s *session) convertReceived(received codex.Received) ([]agent.Event, error) {
	switch received.Kind {
	case codex.ReceivedEvent:
		return s.convertEvent(received)
	case codex.ReceivedRequest:
		return s.convertRequest(received)
	case codex.ReceivedObservation:
		return s.nativeObservation(received)
	default:
		return nil, nil
	}
}

func (s *session) convertEvent(received codex.Received) ([]agent.Event, error) {
	event := received.Event
	if event == nil {
		return nil, nil
	}
	if event.DecodeError != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			"event_decode_failed",
			"decode native Codex event",
			event.DecodeError,
		)
	}

	if received.Source.ThreadID != "" && received.Source.ThreadID != s.ID() {
		switch event.Method {
		case "item/agentMessage/delta":
			return s.agentMessageDelta(received)
		case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
			return s.reasoningDelta(received)
		case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
			return s.toolOutputDelta(received)
		case "item/started":
			return s.itemStarted(received)
		case "item/completed":
			return s.itemCompleted(received)
		case "turn/plan/updated":
			return s.planUpdated(received)
		case "error":
			return s.nativeError(received)
		case "serverRequest/resolved":
			return s.resolveInteraction(received)
		default:
			return s.notificationGap(
				received,
				"child_thread_notification_unsupported",
				"Codex child-thread notification has no unified session-level payload",
			)
		}
	}
	if mainTurnScopedNotification(event.Method) &&
		!s.ownsMainTurn(received.Source.TurnID) {
		return s.notificationGap(
			received,
			"stale_native_turn",
			"Codex notification does not belong to the current session work",
		)
	}

	switch event.Method {
	case "turn/started":
		return s.turnStarted(received)
	case "turn/completed":
		return s.turnCompleted(received)
	case "thread/status/changed":
		return s.threadStatusChanged(received)
	case "item/agentMessage/delta":
		return s.agentMessageDelta(received)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		return s.reasoningDelta(received)
	case "item/reasoning/summaryPartAdded":
		return s.notificationGap(
			received,
			"reasoning_part_boundary_unsupported",
			"Codex reasoning part boundaries have no unified payload",
		)
	case "item/plan/delta":
		return s.notificationGap(
			received,
			"plan_delta_unsupported",
			"Codex plan text deltas have no unified payload",
		)
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		return s.toolOutputDelta(received)
	case "item/started":
		return s.itemStarted(received)
	case "item/completed":
		return s.itemCompleted(received)
	case "turn/plan/updated":
		return s.planUpdated(received)
	case "thread/name/updated":
		return s.threadNameUpdated(received)
	case "thread/tokenUsage/updated":
		return s.tokenUsageUpdated(received)
	case "thread/settings/updated":
		return s.notificationGap(
			received,
			"config_options_unsupported",
			"Codex settings expose current values but no selectable option catalog",
		)
	case "thread/archived", "thread/unarchived":
		return s.notificationGap(
			received,
			"archive_state_unsupported",
			"Codex archive notifications have no unified payload",
		)
	case "serverRequest/resolved":
		return s.resolveInteraction(received)
	case "error":
		return s.nativeError(received)
	default:
		return s.notificationGap(
			received,
			"unsupported_native_notification",
			"Codex native notification is not mapped to a unified event",
		)
	}
}

func mainTurnScopedNotification(method string) bool {
	switch method {
	case "item/agentMessage/delta",
		"item/reasoning/summaryTextDelta",
		"item/reasoning/textDelta",
		"item/reasoning/summaryPartAdded",
		"item/plan/delta",
		"item/commandExecution/outputDelta",
		"item/fileChange/outputDelta",
		"item/started",
		"item/completed",
		"turn/plan/updated":
		return true
	default:
		return false
	}
}

func (s *session) nativeObservation(received codex.Received) ([]agent.Event, error) {
	observation := received.Observation
	if observation == nil {
		return nil, nil
	}
	if observation.Reason == "parent-confirmed" &&
		observation.ThreadID != "" && observation.ParentThreadID != "" {
		s.parentMu.Lock()
		s.parents[observation.ThreadID] = observation.ParentThreadID
		s.parentMu.Unlock()
	}
	switch observation.Reason {
	case "member-confirmed", "parent-confirmed":
		// Successful lineage observations are represented on the child events
		// they qualify. Other observations are explicit non-fatal gaps below.
		return nil, nil
	}
	extras := map[string]any{
		"observationReason": observation.Reason,
		"threadId":          observation.ThreadID,
	}
	if observation.ParentThreadID != "" {
		extras["parentThreadId"] = observation.ParentThreadID
	}
	event, err := s.sessionErrorEvent(
		received,
		observation.Method,
		"native_observation",
		"Codex SDK observation: "+observation.Reason,
		extras,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) parentThread(threadID string) string {
	if threadID == "" {
		return ""
	}
	s.parentMu.RLock()
	defer s.parentMu.RUnlock()
	return s.parents[threadID]
}

func (s *session) ownsMainTurn(turnID string) bool {
	if turnID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A pristine session may receive a live turn-scoped delta after Resume or
	// before the local Submit response. Keep that native fact without treating
	// the delta as work start; once a turn has been observed, unrelated turns
	// are stale.
	return s.activeTurn == turnID ||
		s.idleTurn == turnID ||
		(s.activeTurn == "" &&
			(s.submitting || s.submitUnknown) &&
			s.lastFinished != turnID) ||
		(s.activeTurn == "" &&
			s.idleTurn == "" &&
			s.lastFinished == "")
}

func (s *session) turnStarted(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.TurnStartedNotification)
	if !ok || data.Turn.ID == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, nil
	}
	owned := s.activeTurn == data.Turn.ID ||
		(s.activeTurn == "" &&
			(s.submitting || s.submitUnknown) &&
			s.lastFinished != data.Turn.ID) ||
		(s.activeTurn == "" &&
			s.idleTurn == "" &&
			s.lastFinished == "")
	if !owned ||
		s.lastFinished == data.Turn.ID ||
		s.startedTurn == data.Turn.ID ||
		s.idleTurn == data.Turn.ID {
		s.mu.Unlock()
		return s.notificationGap(
			received,
			"stale_native_turn",
			"Codex turn start does not belong to the current session work",
		)
	}
	previous := cloneState(s.state)
	s.activeTurn = data.Turn.ID
	s.startedTurn = data.Turn.ID
	s.idleTurn = ""
	s.noteWorkEvidenceLocked()
	running := agent.StateRunning
	if s.blockingInteractionsLocked() != 0 {
		running = agent.StateRequiresAction
	}
	s.state = &running
	s.mu.Unlock()

	payload := agent.StateUpdatePayload{
		PreviousState: previous,
		State:         &running,
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		payload,
		optionalSeconds(data.Turn.StartedAt),
		turnMeta(data.Turn),
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) turnCompleted(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.TurnCompletedNotification)
	if !ok || data.Turn.ID == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	at := optionalSeconds(data.Turn.CompletedAt)

	s.mu.Lock()
	owned := s.activeTurn == data.Turn.ID ||
		s.idleTurn == data.Turn.ID ||
		(s.activeTurn == "" &&
			(s.submitting || s.submitUnknown) &&
			s.lastFinished != data.Turn.ID)
	stale := !owned
	previous := cloneState(s.state)
	var interactions []*pendingInteraction
	var nextState agent.SessionState
	if owned {
		if s.activeTurn == data.Turn.ID {
			s.activeTurn = ""
		}
		if s.startedTurn == data.Turn.ID {
			s.startedTurn = ""
		}
		if s.idleTurn == data.Turn.ID {
			s.idleTurn = ""
		}
		s.noteWorkEvidenceLocked()
		s.lastFinished = data.Turn.ID
		interactions = s.takeTurnInteractionsLocked(data.Turn.ID)
		nextState = agent.StateIdle
		if s.blockingInteractionsLocked() != 0 {
			nextState = agent.StateRequiresAction
		}
		s.state = &nextState
	}
	s.mu.Unlock()

	if stale {
		gap, err := s.notificationGap(
			received,
			"stale_native_turn",
			"Codex turn completion does not belong to the current session work",
		)
		if err != nil {
			return nil, err
		}
		return gap, nil
	}

	events := s.closedInteractionEvents(
		interactions,
		agent.InteractionExpired,
		"turn_completed",
		at,
	)
	var observed *agent.ObservedError
	if turnError, present := data.Turn.Error.Get(); present {
		observed = &agent.ObservedError{Message: turnError.Message}
	}
	payload := agent.StateUpdatePayload{
		PreviousState: previous,
		State:         &nextState,
		StopReason:    string(data.Turn.Status),
		Error:         observed,
	}
	stateEvent, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		payload,
		at,
		turnMeta(data.Turn),
	)
	if err != nil {
		return nil, err
	}
	return append(events, stateEvent), nil
}

func (s *session) threadStatusChanged(received codex.Received) ([]agent.Event, error) {
	var data struct {
		Status codextypes.ThreadStatus `json:"status"`
	}
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	var state *agent.SessionState
	stopReason := ""
	switch {
	case data.Status.Active != nil:
		running := agent.StateRunning
		if len(data.Status.Active.ActiveFlags) != 0 {
			running = agent.StateRequiresAction
		}
		state = &running
	case data.Status.Idle != nil:
		idle := agent.StateIdle
		state = &idle
	case data.Status.NotLoaded != nil:
		stopReason = "notLoaded"
	case data.Status.SystemError != nil:
		stopReason = "systemError"
	case data.Status.Unknown != nil:
		stopReason = "unknown"
	default:
		return nil, invalidNativeEvent(received.Event.Method)
	}

	rootThreadID := s.ID()
	s.mu.Lock()
	previous := cloneState(s.state)
	if state != nil && *state == agent.StateRunning &&
		s.blockingInteractionsLocked() != 0 {
		requiresAction := agent.StateRequiresAction
		state = &requiresAction
	}
	var interactions []*pendingInteraction
	idleObserved := state != nil && *state == agent.StateIdle
	if idleObserved {
		if s.activeTurn != "" {
			s.idleTurn = s.activeTurn
		}
		s.activeTurn = ""
		s.startedTurn = ""
		if !s.submitting {
			s.noteWorkEvidenceLocked()
		}
		interactions = s.takeThreadInteractionsLocked(rootThreadID)
		if s.blockingInteractionsLocked() != 0 {
			requiresAction := agent.StateRequiresAction
			state = &requiresAction
		}
	} else if state == nil {
		interactions = s.takeAllInteractionsLocked()
	}
	s.state = cloneState(state)
	s.mu.Unlock()
	outcome := agent.InteractionCancelled
	reason := "thread_unavailable"
	if idleObserved {
		outcome = agent.InteractionExpired
		reason = "thread_idle"
	}
	events := s.closedInteractionEvents(
		interactions,
		outcome,
		reason,
		time.Now().UTC(),
	)
	event, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		agent.StateUpdatePayload{
			PreviousState: previous,
			State:         state,
			StopReason:    stopReason,
		},
		time.Now().UTC(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return append(events, event), nil
}

func (s *session) agentMessageDelta(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.AgentMessageDeltaNotification)
	if !ok || data.ItemID == "" || data.TurnID == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		data.ItemID,
		agent.MessageChunk{
			MessageID: data.ItemID,
			Content:   []agent.ContentBlock{agent.TextBlock{Text: data.Delta}},
		},
		time.Now().UTC(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) reasoningDelta(received codex.Received) ([]agent.Event, error) {
	var data itemDeltaNotification
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	if data.ItemID == "" || data.TurnID == "" || data.Delta == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		data.ItemID,
		agent.ThinkingChunk{
			MessageID: data.ItemID,
			Content:   []agent.ContentBlock{agent.TextBlock{Text: data.Delta}},
		},
		time.Now().UTC(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) toolOutputDelta(received codex.Received) ([]agent.Event, error) {
	var data itemDeltaNotification
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	if data.ItemID == "" || data.TurnID == "" || data.Delta == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		data.ItemID,
		agent.ToolChunkPayload{
			ID:      data.ItemID,
			Content: []agent.ContentBlock{agent.TextBlock{Text: data.Delta}},
		},
		time.Now().UTC(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) itemStarted(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.ItemStartedNotification)
	if !ok {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	item := data.Item
	switch {
	case item.UserMessage != nil, item.AgentMessage != nil, item.Reasoning != nil:
		return nil, nil
	default:
		return s.toolCall(received, data.StartedAtMs, data.Item)
	}
}

func (s *session) itemCompleted(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.ItemCompletedNotification)
	if !ok {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	item := data.Item
	switch {
	case item.UserMessage != nil:
		return s.userMessage(received, data.CompletedAtMs, item.UserMessage)
	case item.AgentMessage != nil:
		return s.agentMessage(received, data.CompletedAtMs, item.AgentMessage)
	case item.Reasoning != nil:
		return s.reasoningMessage(received, data.CompletedAtMs, item.Reasoning)
	default:
		return s.toolResult(received, data.CompletedAtMs, data.Item)
	}
}

func (s *session) userMessage(
	received codex.Received,
	completedAtMs int64,
	item *codextypes.UserMessageThreadItem,
) ([]agent.Event, error) {
	if item.ID == "" || len(item.Content) == 0 {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	content := make([]agent.ContentBlock, 0, len(item.Content))
	for _, input := range item.Content {
		if input.Text == nil || len(input.Unknown) != 0 {
			return s.itemGap(received, codextypes.ThreadItem{UserMessage: item})
		}
		content = append(content, agent.TextBlock{Text: input.Text.Text})
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		item.ID,
		agent.UserMessage{MessageID: item.ID, Content: content},
		unixMilliOrNow(completedAtMs),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) agentMessage(
	received codex.Received,
	completedAtMs int64,
	item *codextypes.AgentMessageThreadItem,
) ([]agent.Event, error) {
	if item.ID == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	phase := ""
	if value, present := item.Phase.Get(); present {
		phase = string(value)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		item.ID,
		agent.AgentMessage{
			MessageID: item.ID,
			Content:   []agent.ContentBlock{agent.TextBlock{Text: item.Text}},
			Phase:     phase,
		},
		unixMilliOrNow(completedAtMs),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) reasoningMessage(
	received codex.Received,
	completedAtMs int64,
	item *codextypes.ReasoningThreadItem,
) ([]agent.Event, error) {
	if item.ID == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	parts, present := item.Summary.Get()
	if !present || len(parts) == 0 {
		parts, present = item.Content.Get()
	}
	if !present || len(parts) == 0 {
		return s.itemGap(received, codextypes.ThreadItem{Reasoning: item})
	}
	content := make([]agent.ContentBlock, 0, len(parts))
	for _, text := range parts {
		content = append(content, agent.TextBlock{Text: text})
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		item.ID,
		agent.AgentThinking{MessageID: item.ID, Content: content},
		unixMilliOrNow(completedAtMs),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) planUpdated(received codex.Received) ([]agent.Event, error) {
	var data planUpdatedNotification
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	if data.TurnID == "" || len(data.Plan) == 0 {
		return s.notificationGap(
			received,
			"plan_shape_unsupported",
			"Codex plan update cannot be represented as unified plan entries",
		)
	}
	for _, step := range data.Plan {
		status := unifiedPlanStatus(step.Status)
		if step.Step == "" || status == "" {
			return s.notificationGap(
				received,
				"plan_shape_unsupported",
				"Codex plan update contains an unsupported entry",
			)
		}
	}
	extras := map[string]any{
		"nativeItemIdAbsent": true,
		"planId":             data.TurnID,
		"planEntryCount":     len(data.Plan),
	}
	if data.Explanation != "" {
		extras["explanation"] = data.Explanation
	}
	// A unified plan event needs both a plan identity and an item identity, but
	// turn/plan/updated is turn-scoped and carries no item ID. Keep the native
	// fact and explanation in a non-fatal gap instead of inventing an item.
	event, err := s.sessionErrorEvent(
		received,
		received.Event.Method,
		"plan_identity_unavailable",
		"Codex turn plan has no native item ID for a unified plan event",
		extras,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) threadNameUpdated(received codex.Received) ([]agent.Event, error) {
	var data threadNameNotification
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	if data.ThreadName == nil || *data.ThreadName == "" {
		return s.notificationGap(
			received,
			"thread_name_unrepresentable",
			"Codex thread name update has no nonempty title",
		)
	}
	title := *data.ThreadName
	event, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		agent.InfoUpdatePayload{Title: &title},
		time.Now().UTC(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) tokenUsageUpdated(received codex.Received) ([]agent.Event, error) {
	var data tokenUsageNotification
	if err := json.Unmarshal(received.Event.Params, &data); err != nil {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	if data.TokenUsage.Last.TotalTokens < 0 ||
		data.TokenUsage.Total.TotalTokens < 0 ||
		(data.TokenUsage.ModelContextWindow != nil && *data.TokenUsage.ModelContextWindow < 0) {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	size := int64(0)
	if data.TokenUsage.ModelContextWindow != nil {
		size = *data.TokenUsage.ModelContextWindow
	}
	extras := map[string]any{
		"totalTokens":              data.TokenUsage.Total.TotalTokens,
		"modelContextWindowAbsent": size == 0,
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		agent.UsageUpdatePayload{
			Used: data.TokenUsage.Last.TotalTokens,
			Size: size,
		},
		time.Now().UTC(),
		extras,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) nativeError(received codex.Received) ([]agent.Event, error) {
	data, ok := received.Event.Data.(*codextypes.ErrorNotification)
	if !ok || data.Error.Message == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		"",
		agent.SessionErrorPayload{
			Message: data.Error.Message,
			Reason:  "native_error",
		},
		time.Now().UTC(),
		map[string]any{"willRetry": data.WillRetry},
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) itemGap(
	received codex.Received,
	item codextypes.ThreadItem,
) ([]agent.Event, error) {
	itemType, itemID := threadItemIdentity(item)
	message := "Codex item variant has no unified payload"
	if itemType != "" {
		message = "Codex item variant " + itemType + " has no unified payload"
	}
	event, err := s.sessionErrorEvent(
		received,
		requestMethod(received),
		"unsupported_native_item",
		message,
		nil,
	)
	if err != nil {
		return nil, err
	}
	event.ItemID = itemID
	return []agent.Event{event}, nil
}

func (s *session) notificationGap(
	received codex.Received,
	reason string,
	message string,
) ([]agent.Event, error) {
	event, err := s.sessionErrorEvent(
		received,
		requestMethod(received),
		reason,
		message,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) sessionErrorEvent(
	received codex.Received,
	method string,
	reason string,
	message string,
	extras map[string]any,
) (agent.Event, error) {
	return s.newEvent(
		received,
		method,
		"",
		agent.SessionErrorPayload{Message: message, Reason: reason},
		time.Now().UTC(),
		extras,
	)
}

func (s *session) newEvent(
	received codex.Received,
	method string,
	itemID string,
	payload agent.Payload,
	eventTime time.Time,
	extras map[string]any,
) (agent.Event, error) {
	eventID, err := uuidv4.New()
	if err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_id_failed",
			"create unified event identity",
			err,
		)
	}
	if method == "" {
		method = requestMethod(received)
	}
	raw := received.Raw
	if len(raw) == 0 && received.Event != nil {
		raw = received.Event.Raw
	}
	if eventTime.IsZero() {
		eventTime = time.Now().UTC()
	}
	codexMeta := map[string]any{
		"method": method,
		"raw":    append(json.RawMessage(nil), raw...),
	}
	sessionID := s.ID()
	if received.Source.ThreadID != "" && received.Source.ThreadID != sessionID {
		codexMeta["sourceThreadId"] = received.Source.ThreadID
		if parentThreadID := s.parentThread(received.Source.ThreadID); parentThreadID != "" {
			codexMeta["parentThreadId"] = parentThreadID
		}
	}
	if received.Source.TurnID != "" {
		codexMeta["turnId"] = received.Source.TurnID
	}
	if received.Source.ItemID != "" {
		codexMeta["itemId"] = received.Source.ItemID
	}
	if received.Request != nil {
		if encoded, err := json.Marshal(received.Request.ID()); err == nil {
			codexMeta["requestId"] = json.RawMessage(encoded)
		}
	}
	for key, value := range extras {
		if _, reserved := codexMeta[key]; reserved {
			if (key != "turnId" && key != "itemId") || codexMeta[key] != "" {
				continue
			}
		}
		codexMeta[key] = value
	}
	event := agent.Event{
		EventID:   eventID,
		SessionID: sessionID,
		Time:      eventTime,
		ItemID:    itemID,
		Payload:   payload,
		Meta: map[string]any{
			"codex": codexMeta,
		},
	}
	if err := event.Validate(); err != nil {
		return agent.Event{}, adapterError(
			agent.ErrorUnavailable,
			"event_invalid",
			"convert native Codex event",
			err,
		)
	}
	return event, nil
}

func optionalSeconds(value codextypes.Optional[int64]) time.Time {
	seconds, ok := value.Get()
	if !ok || seconds <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(seconds, 0).UTC()
}

func unixMilliOrNow(value int64) time.Time {
	if value <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(value).UTC()
}

func invalidNativeEvent(method string) error {
	return adapterError(
		agent.ErrorUnavailable,
		"invalid_native_event",
		"invalid native Codex event: "+method,
		nil,
	)
}

func cloneState(value *agent.SessionState) *agent.SessionState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func turnMeta(turn codextypes.Turn) map[string]any {
	if root, present := turn.RootTurnID.Get(); present {
		return map[string]any{"rootTurnId": root}
	}
	return nil
}

func toolStatus(value string) string {
	switch value {
	case "inProgress":
		return "in_progress"
	case "completed", "failed", "declined":
		return value
	default:
		return "unknown"
	}
}

func unifiedPlanStatus(value string) string {
	switch value {
	case "pending":
		return "pending"
	case "inProgress":
		return "in_progress"
	case "completed":
		return "completed"
	default:
		return ""
	}
}

func threadItemIdentity(item codextypes.ThreadItem) (string, string) {
	switch {
	case item.UserMessage != nil:
		return "userMessage", item.UserMessage.ID
	case item.HookPrompt != nil:
		return "hookPrompt", item.HookPrompt.ID
	case item.AgentMessage != nil:
		return "agentMessage", item.AgentMessage.ID
	case item.FunctionCallOutput != nil:
		return "functionCallOutput", item.FunctionCallOutput.ID
	case item.Plan != nil:
		return "plan", item.Plan.ID
	case item.Reasoning != nil:
		return "reasoning", item.Reasoning.ID
	case item.CommandExecution != nil:
		return "commandExecution", item.CommandExecution.ID
	case item.FileChange != nil:
		return "fileChange", item.FileChange.ID
	case item.MCPToolCall != nil:
		return "mcpToolCall", item.MCPToolCall.ID
	case item.DynamicToolCall != nil:
		return "dynamicToolCall", item.DynamicToolCall.ID
	case item.CollabAgentToolCall != nil:
		return "collabAgentToolCall", item.CollabAgentToolCall.ID
	case item.SubAgentActivity != nil:
		return "subAgentActivity", item.SubAgentActivity.ID
	case item.WebSearch != nil:
		return "webSearch", item.WebSearch.ID
	case item.ImageView != nil:
		return "imageView", item.ImageView.ID
	case item.Sleep != nil:
		return "sleep", item.Sleep.ID
	case item.ImageGeneration != nil:
		return "imageGeneration", item.ImageGeneration.ID
	case item.EnteredReviewMode != nil:
		return "enteredReviewMode", item.EnteredReviewMode.ID
	case item.ExitedReviewMode != nil:
		return "exitedReviewMode", item.ExitedReviewMode.ID
	case item.ContextCompaction != nil:
		return "contextCompaction", item.ContextCompaction.ID
	case len(item.Unknown) != 0:
		var tagged struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		_ = json.Unmarshal(item.Unknown, &tagged)
		return tagged.Type, tagged.ID
	default:
		return "", ""
	}
}

type itemDeltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type planUpdatedNotification struct {
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId"`
	Explanation string `json:"explanation"`
	Plan        []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	} `json:"plan"`
}

type threadNameNotification struct {
	ThreadID   string  `json:"threadId"`
	ThreadName *string `json:"threadName"`
}

type tokenUsageNotification struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Total struct {
			TotalTokens int64 `json:"totalTokens"`
		} `json:"total"`
		Last struct {
			TotalTokens int64 `json:"totalTokens"`
		} `json:"last"`
		ModelContextWindow *int64 `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}
