// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

const sessionStartProbeReservedPrefix = "agent-sdk-go:"

// SessionStartProbe registers one SDK-private SessionStart callback matcher.
// It is not a general hook callback API: matching native callbacks are answered
// with an empty HookJSONOutput and remain observable only as control events.
// CallbackID must be fresh for each probe and use the SDK-reserved
// "agent-sdk-go:" namespace.
type SessionStartProbe struct {
	Source     string
	CallbackID string
}

type hookMatcherWire struct {
	Matcher         string   `json:"matcher"`
	HookCallbackIDs []string `json:"hookCallbackIds"`
}

func (o *Options) applySessionStartProbe() error {
	probe := o.SessionStartProbe
	if probe == nil {
		return nil
	}
	if probe.Source == "" || !utf8.ValidString(probe.Source) {
		return fmt.Errorf("%w: SessionStart probe source must be nonempty UTF-8", ErrInvalidInput)
	}
	if probe.CallbackID == "" || !utf8.ValidString(probe.CallbackID) ||
		!strings.HasPrefix(probe.CallbackID, sessionStartProbeReservedPrefix) {
		return fmt.Errorf("%w: SessionStart probe callback ID must use the %q namespace", ErrInvalidInput, sessionStartProbeReservedPrefix)
	}

	var hooks json.RawMessage
	if o.Initialize != nil {
		hooks = o.Initialize.Hooks
	}
	hooks, err := appendSessionStartProbe(hooks, *probe)
	if err != nil {
		return err
	}

	initialize := types.InitializeOptions{Hooks: hooks}
	if o.Initialize != nil {
		initialize = *o.Initialize
		initialize.Hooks = hooks
	} else {
		forward := true
		initialize.ForwardSubagentText = &forward
	}
	o.Initialize = &initialize
	o.sessionStartProbeCallbackID = probe.CallbackID
	// Options.defaults runs once in Open and again while opening the interaction.
	// Clearing the applied request makes that second pass idempotent.
	o.SessionStartProbe = nil
	return nil
}

func appendSessionStartProbe(raw json.RawMessage, probe SessionStartProbe) (json.RawMessage, error) {
	hooks, err := decodeHooksObject(raw)
	if err != nil {
		return nil, err
	}
	for event, entries := range hooks {
		if _, err := validateHookMatchers(event, entries); err != nil {
			return nil, err
		}
	}

	matcher, err := json.Marshal(hookMatcherWire{
		Matcher:         probe.Source,
		HookCallbackIDs: []string{probe.CallbackID},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode SessionStart probe matcher", ErrInvalidInput)
	}
	var sessionStart []json.RawMessage
	if raw, ok := hooks["SessionStart"]; ok {
		if err := json.Unmarshal(raw, &sessionStart); err != nil {
			return nil, fmt.Errorf("%w: SessionStart hooks must be an array", ErrInvalidInput)
		}
	}
	sessionStart = append(sessionStart, matcher)
	encoded, err := json.Marshal(sessionStart)
	if err != nil {
		return nil, fmt.Errorf("%w: encode SessionStart hooks", ErrInvalidInput)
	}
	hooks["SessionStart"] = encoded

	encoded, err = json.Marshal(hooks)
	if err != nil {
		return nil, fmt.Errorf("%w: encode hooks", ErrInvalidInput)
	}
	return encoded, nil
}

func decodeHooksObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}
	if !utf8.Valid(trimmed) || !json.Valid(trimmed) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: hooks must be a JSON object or null", ErrInvalidInput)
	}
	if err := validateUniqueJSONMembers(trimmed); err != nil {
		return nil, fmt.Errorf("%w: hooks: %w", ErrInvalidInput, err)
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &hooks); err != nil || hooks == nil {
		return nil, fmt.Errorf("%w: hooks must be a JSON object or null", ErrInvalidInput)
	}
	return hooks, nil
}

func validateHookMatchers(event string, raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("%w: hooks[%q] must be an array", ErrInvalidInput, event)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("%w: hooks[%q] must be an array", ErrInvalidInput, event)
	}
	for index, entry := range entries {
		if err := validateHookMatcher(event, index, entry); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func validateHookMatcher(event string, index int, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: hooks[%q][%d] must be an object", ErrInvalidInput, event, index)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return fmt.Errorf("%w: hooks[%q][%d] must be an object", ErrInvalidInput, event, index)
	}
	if matcher, ok := fields["matcher"]; ok {
		var value string
		matcher = bytes.TrimSpace(matcher)
		if !bytes.Equal(matcher, []byte("null")) &&
			(!isJSONString(matcher) || json.Unmarshal(matcher, &value) != nil) {
			return fmt.Errorf("%w: hooks[%q][%d].matcher must be a string or null", ErrInvalidInput, event, index)
		}
	}
	ids, ok := fields["hookCallbackIds"]
	if !ok {
		return fmt.Errorf("%w: hooks[%q][%d].hookCallbackIds is required", ErrInvalidInput, event, index)
	}
	var callbackIDs []json.RawMessage
	if !isJSONArray(ids) || json.Unmarshal(ids, &callbackIDs) != nil {
		return fmt.Errorf("%w: hooks[%q][%d].hookCallbackIds must be an array of strings", ErrInvalidInput, event, index)
	}
	for callbackIndex, callbackID := range callbackIDs {
		var value string
		if !isJSONString(callbackID) || json.Unmarshal(callbackID, &value) != nil {
			return fmt.Errorf("%w: hooks[%q][%d].hookCallbackIds[%d] must be a string", ErrInvalidInput, event, index, callbackIndex)
		}
		if strings.HasPrefix(value, sessionStartProbeReservedPrefix) {
			return fmt.Errorf("%w: caller hook callback ID uses the reserved %q namespace", ErrInvalidInput, sessionStartProbeReservedPrefix)
		}
	}
	if timeout, ok := fields["timeout"]; ok {
		timeout = bytes.TrimSpace(timeout)
		if len(timeout) == 0 || (timeout[0] != '-' && (timeout[0] < '0' || timeout[0] > '9')) {
			return fmt.Errorf("%w: hooks[%q][%d].timeout must be a number", ErrInvalidInput, event, index)
		}
		var number json.Number
		if json.Unmarshal(timeout, &number) != nil {
			return fmt.Errorf("%w: hooks[%q][%d].timeout must be a number", ErrInvalidInput, event, index)
		}
	}
	return nil
}

func isJSONString(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}

func isJSONArray(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '['
}

func validateUniqueJSONMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return validateUniqueJSONValue(decoder)
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid object member")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate JSON object member")
			}
			seen[name] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case json.Delim('['):
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return nil
	}
}

func (c *interaction) ownsSessionStartProbe(request *ControlRequest) (string, bool) {
	callbackID := c.opts.sessionStartProbeCallbackID
	if callbackID == "" || request.Subtype != "hook_callback" {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(request.Request, &fields) != nil {
		return "", false
	}
	raw, ok := fields["callback_id"]
	if !ok || !isJSONString(raw) {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value != callbackID {
		return "", false
	}
	return request.RequestID, true
}

func (c *interaction) answerSessionStartProbe(requestID string) error {
	return c.writeJSON(context.Background(), map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   map[string]any{},
		},
	})
}
