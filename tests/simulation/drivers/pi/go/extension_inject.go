package pisim

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
)

func (f extensionFlow) runInjection(code string) error {
	injector, ok := f.session.(agent.Injector)
	if !ok {
		return errors.New("unified Pi session has no Injector")
	}
	original := injectTurn{prompt: "initial controlled query", reply: "Ready"}
	active := code == "038"
	if active {
		original = injectTurn{prompt: "hold active turn", reply: "First token ended"}
	}
	if err := f.prompt(original.prompt); err != nil {
		return err
	}
	if err := f.collectInjectTurn(&original, active, nil); err != nil {
		return err
	}

	injection := injectAppend{fact: "controlled-pi-inject-fact-" + code}
	receipt, err := injector.Inject(f.ctx, extensionTextInput(injection.fact))
	if err != nil {
		return fmt.Errorf("inject Pi context: %w", err)
	}
	if !nativeAccepted(receipt.Acceptance) {
		return errors.New("pi: Inject receipt did not confirm both the write and native routing")
	}
	if err := f.output.Encode(map[string]any{"type": "inject.receipt", "receipt": receipt, "active": active}); err != nil {
		return err
	}
	if active {
		if err := f.collectInjectTurn(&original, false, &injection); err != nil {
			return err
		}
	} else {
		for _, want := range []string{"message_start", "message_end"} {
			event, err := f.next()
			if err != nil {
				return err
			}
			if nativeEventType(event) != want {
				return fmt.Errorf("pi: expected idle Inject %s, got %s", want, nativeEventType(event))
			}
			if err := injection.observe(event); err != nil {
				return err
			}
		}
	}
	if err := f.output.Encode(map[string]any{
		"type": "inject.append", "fact": injection.fact, "eventId": injection.endID, "originalTurnEndId": original.endID,
	}); err != nil {
		return err
	}

	query := injectTurn{prompt: "recover injected fact", reply: injection.fact}
	if err := f.prompt(query.prompt); err != nil {
		return err
	}
	if err := f.collectInjectTurn(&query, false, nil); err != nil {
		return err
	}
	return f.output.Encode(map[string]any{
		"type": "inject.consumption", "originalReply": original.text, "originalReplyContainsFact": strings.Contains(original.text, injection.fact),
		"queryReply": query.text, "queryReplyContainsFact": query.text == injection.fact,
	})
}

type injectTurn struct {
	prompt, reply string
	running       bool
	turnStarted   bool
	userSeen      bool
	chunks        strings.Builder
	messageID     string
	text, endID   string
	settled       bool
}

// Reading the first chunk leaves the original turn open. The controlled
// recorder holds its model response at this point through the Inject receipt.
func (f extensionFlow) collectInjectTurn(turn *injectTurn, firstChunk bool, pending *injectAppend) error {
	for !turn.settled {
		event, err := f.next()
		if err != nil {
			return err
		}
		if err := turn.observe(event, pending); err != nil {
			return fmt.Errorf("pi: %s: %w", turn.prompt, err)
		}
		if firstChunk && turn.chunks.Len() != 0 {
			if turn.chunks.String() != "First token" {
				return errors.New("pi: active turn did not yield the expected first chunk")
			}
			return nil
		}
	}
	if firstChunk {
		return errors.New("pi: active turn settled before the first chunk")
	}
	return nil
}

func (t *injectTurn) observe(event agent.Event, pending *injectAppend) error {
	switch nativeEventType(event) {
	case "agent_start":
		state, ok := event.Payload.(agent.StateUpdatePayload)
		if !ok || t.running || !inState(state, agent.StateRunning) {
			return errors.New("unexpected run start")
		}
		t.running = true
	case "turn_start":
		if !t.running || t.turnStarted {
			return errors.New("unexpected native turn start")
		}
		t.turnStarted = true
	case "message_start", "message_end":
		if isInjectMessage(event) {
			if pending == nil || t.endID == "" {
				return errors.New("observed an injection outside its expected turn boundary")
			}
			return pending.observe(event)
		}
		switch message := event.Payload.(type) {
		case agent.UserMessage:
			if !t.turnStarted || t.userSeen || extensionText(message.Content) != t.prompt {
				return errors.New("unexpected user message")
			}
			t.userSeen = true
		case agent.AgentMessage:
			if !t.userSeen || t.text != "" || t.messageID != message.MessageID || extensionText(message.Content) != t.reply {
				return errors.New("unexpected completed assistant message")
			}
			t.text = t.reply
		}
	case "message_update":
		if chunk, ok := event.Payload.(agent.MessageChunk); ok {
			if !t.userSeen || t.text != "" || (t.messageID != "" && t.messageID != chunk.MessageID) {
				return errors.New("unexpected assistant chunk")
			}
			t.messageID = chunk.MessageID
			t.chunks.WriteString(extensionText(chunk.Content))
		}
	case "turn_end":
		if t.endID != "" || t.text != t.reply || t.chunks.String() != t.reply {
			return errors.New("unexpected native turn end")
		}
		t.endID = event.EventID
	case "agent_settled":
		state, ok := event.Payload.(agent.StateUpdatePayload)
		if !ok || !inState(state, agent.StateIdle) || state.StopReason != "stop" || state.Error != nil ||
			t.endID == "" || (pending != nil && pending.endID == "") {
			return errors.New("run settled without the expected reply, turn boundary, or injection append")
		}
		t.settled = true
	case "tool_execution_start", "extension_ui_request":
		return errors.New("unexpected tool or interaction in the Inject flow")
	}
	return nil
}

type injectAppend struct {
	fact, endID string
	timestamp   int64
}

type injectNativeMessage struct {
	Role, CustomType string
	Content          []struct{ Type, Text string }
	Display          *bool
	Timestamp        int64
}

func isInjectMessage(event agent.Event) bool {
	payload, ok := event.Payload.(agent.UnsupportedNativeEventPayload)
	if !ok || payload.AgentType != "pi" {
		return false
	}
	var frame struct {
		Message struct{ CustomType string }
	}
	return json.Unmarshal(payload.Raw, &frame) == nil && frame.Message.CustomType == "agent-sdk-inject"
}

func (i *injectAppend) observe(event agent.Event) error {
	payload, ok := event.Payload.(agent.UnsupportedNativeEventPayload)
	var frame struct {
		Type    string
		Message injectNativeMessage
	}
	if !ok || payload.AgentType != "pi" || json.Unmarshal(payload.Raw, &frame) != nil || frame.Type != nativeEventType(event) {
		return errors.New("pi: missing native Inject message")
	}
	message := frame.Message
	if message.Role != "custom" || message.CustomType != "agent-sdk-inject" || message.Display == nil || *message.Display ||
		message.Timestamp <= 0 || len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != i.fact {
		return errors.New("pi: native Inject message did not match the hidden appended fact")
	}
	switch frame.Type {
	case "message_start":
		if i.timestamp != 0 {
			return errors.New("pi: native Inject message started twice")
		}
		i.timestamp = message.Timestamp
	case "message_end":
		if i.timestamp != message.Timestamp || i.endID != "" {
			return errors.New("pi: native Inject message did not end its original append")
		}
		i.endID = event.EventID
	default:
		return errors.New("pi: unexpected native Inject message type")
	}
	return nil
}
