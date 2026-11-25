// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// EffortChange is the native runtime effort flag request. Nil Level clears the
// flag-layer effort override with null. A nonnil value must be nonempty and is
// passed literally. SetEffort confirms the current flag state first and closes
// ultracode when it would override a different requested effort. Unrelated flag
// settings remain intact. It never changes max_thinking_tokens or converts
// effort into a token budget.
type EffortChange struct{ Level *string }

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

// SetPermissionMode requests the native mode and retains its acknowledgement.
// It does not answer or release any pending permission request.
func (s *Session) SetPermissionMode(ctx context.Context, mode PermissionMode) (ConfigurationReceipt, error) {
	if !mode.Valid() {
		return ConfigurationReceipt{}, dispatchFailure("set_permission_mode", fmt.Errorf("%w: unsupported permission mode %q", ErrInvalidInput, mode))
	}
	return s.configure(ctx, "set_permission_mode", map[string]any{"subtype": "set_permission_mode", "mode": mode})
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

func (s *Session) SetEffort(ctx context.Context, change EffortChange) (ConfigurationReceipt, error) {
	fail := func(err error) error { return dispatchFailure("apply_flag_settings", err) }
	if ctx == nil {
		return ConfigurationReceipt{}, fail(queue.ErrNilContext)
	}
	if err := ctx.Err(); err != nil {
		return ConfigurationReceipt{}, fail(err)
	}
	if err := validateInputEncoding(change); err != nil {
		return ConfigurationReceipt{}, fail(err)
	}
	if change.Level != nil && *change.Level == "" {
		return ConfigurationReceipt{}, fail(fmt.Errorf("%w: empty effort level", ErrInvalidInput))
	}

	if err := s.acquireSettingsGate(ctx); err != nil {
		return ConfigurationReceipt{}, fail(err)
	}
	defer s.releaseSettingsGate()

	settings, err := s.client.controlRequest(
		ctx,
		"get_settings",
		map[string]any{"subtype": "get_settings"},
		false,
	)
	if err != nil {
		var controlErr *ControlError
		if errors.Is(err, ErrUnknownOutcome) || errors.As(err, &controlErr) {
			return ConfigurationReceipt{}, &DispatchError{
				Op:    "apply_flag_settings",
				State: NotSent,
				Err:   &effortPreflightError{cause: err},
			}
		}
		return ConfigurationReceipt{}, fail(err)
	}
	ultracode, err := confirmEffortFlagSettings(settings)
	if err != nil {
		return ConfigurationReceipt{}, fail(err)
	}
	ultracode = effortKeepsUltracode(change.Level, ultracode)

	request := map[string]any{
		"subtype": "apply_flag_settings",
		"settings": map[string]any{
			"effortLevel": change.Level,
			"ultracode":   ultracode,
		},
	}
	return s.configure(ctx, "apply_flag_settings", request)
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

func confirmEffortFlagSettings(raw json.RawMessage) (bool, error) {
	unsupported := func(format string, args ...any) error {
		return fmt.Errorf("%w: get_settings cannot confirm %s", ErrUnsupported, fmt.Sprintf(format, args...))
	}

	var settings struct {
		Applied struct {
			Ultracode *bool `json:"ultracode"`
		} `json:"applied"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil || settings.Applied.Ultracode == nil {
		return false, unsupported("the applied ultra code state")
	}
	return *settings.Applied.Ultracode, nil
}

type effortPreflightError struct{ cause error }

func (e *effortPreflightError) Error() string {
	return fmt.Sprintf(
		"%s: cannot confirm current flag settings: %s",
		ErrUnsupported,
		e.cause,
	)
}

func (e *effortPreflightError) Unwrap() error { return ErrUnsupported }

func (e *effortPreflightError) Is(target error) bool {
	if target == ErrUnsupported {
		return true
	}
	if target == ErrUnknownOutcome {
		return false
	}
	return errors.Is(e.cause, target)
}

func (e *effortPreflightError) As(target any) bool {
	return errors.As(e.cause, target)
}

// effortKeepsUltracode retains ultracode only when the requested literal still
// selects it or its xhigh effort. Any other requested level, including a null
// clear, must close ultracode so Claude does not silently replace that effort
// with xhigh.
func effortKeepsUltracode(level *string, current bool) bool {
	if level == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(*level)) {
	case "ultracode":
		return true
	case "xhigh":
		return current
	default:
		return false
	}
}
