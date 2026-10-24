// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// ModelChange preserves SDK 0.3.258 set_model semantics. Nil omits model and
// requests the native default. A nonnil identifier is passed literally; empty
// identifiers are invalid. Null and cross-provider aliases are not synthesized.
type ModelChange struct{ Model *string }

// ThinkingChange is the native dynamic set_max_thinking_tokens request.
// Nil MaxThinkingTokens sends null (clear); a pointer to zero sends 0.
// Nil ThinkingDisplay omits display unless ClearDisplay sends null. Supported
// display values are summarized and omitted. This deprecated budget may act
// as on/off depending on model; it is not a guaranteed hard token ceiling.
type ThinkingChange struct {
	MaxThinkingTokens *int
	ThinkingDisplay   *string
	ClearDisplay      bool
}

// ConfigurationReceipt separates the exact requested control payload, original
// control_response and actual observations. Accepted means native success ack,
// not a barrier for active work or descendants. Observed remains nil: inspect
// later native events without treating requested values as observed settings.
type ConfigurationReceipt struct {
	Requested json.RawMessage
	Response  json.RawMessage
	Accepted  bool
	Observed  json.RawMessage
}

func (s *Session) SetModel(ctx context.Context, change ModelChange) (ConfigurationReceipt, error) {
	request := map[string]any{"subtype": "set_model"}
	if change.Model != nil {
		if *change.Model == "" {
			return ConfigurationReceipt{}, dispatchFailure("set_model", fmt.Errorf("%w: empty model identifier", ErrInvalidInput))
		}
		request["model"] = *change.Model
	}
	return s.configure(ctx, "set_model", request)
}

func (s *Session) SetThinking(ctx context.Context, change ThinkingChange) (ConfigurationReceipt, error) {
	fail := func(err error) (ConfigurationReceipt, error) {
		return ConfigurationReceipt{}, dispatchFailure("set_max_thinking_tokens", err)
	}
	if change.MaxThinkingTokens != nil && *change.MaxThinkingTokens < 0 {
		return fail(fmt.Errorf("%w: negative max_thinking_tokens", ErrInvalidInput))
	}
	if change.ClearDisplay && change.ThinkingDisplay != nil {
		return fail(fmt.Errorf("%w: display value and clear", ErrInvalidInput))
	}
	request := map[string]any{"subtype": "set_max_thinking_tokens", "max_thinking_tokens": change.MaxThinkingTokens}
	if change.ClearDisplay {
		request["thinking_display"] = nil
	}
	if change.ThinkingDisplay != nil {
		if *change.ThinkingDisplay != "summarized" && *change.ThinkingDisplay != "omitted" {
			return fail(fmt.Errorf("%w: thinking_display %q", ErrUnsupported, *change.ThinkingDisplay))
		}
		request["thinking_display"] = *change.ThinkingDisplay
	}
	return s.configure(ctx, "set_max_thinking_tokens", request)
}

func (s *Session) configure(ctx context.Context, subtype string, request any) (ConfigurationReceipt, error) {
	if ctx == nil {
		return ConfigurationReceipt{}, dispatchFailure(subtype, queue.ErrNilContext)
	}
	if err := validateInputEncoding(request); err != nil {
		return ConfigurationReceipt{}, dispatchFailure(subtype, err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return ConfigurationReceipt{}, dispatchFailure(subtype, err)
	}
	r := ConfigurationReceipt{Requested: data}
	r.Response, err = s.client.controlRequest(ctx, subtype, json.RawMessage(data), true)
	r.Accepted = err == nil
	return r, dispatchFailure(subtype, err)
}
