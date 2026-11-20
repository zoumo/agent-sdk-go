package claude

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// GetSettings returns the native get_settings response body without interpreting
// or caching it. It is a read-only control query, not an execution-idle barrier.
func (s *Session) GetSettings(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, dispatchFailure("get_settings", queue.ErrNilContext)
	}
	response, err := s.client.controlRequest(
		ctx, "get_settings", map[string]any{"subtype": "get_settings"}, false,
	)
	return response, dispatchFailure("get_settings", err)
}

// GetEffectiveSandbox reports the Bash sandbox settings from get_settings.
// Omitted fields remain nil. The result describes CLI configuration.
func (s *Session) GetEffectiveSandbox(ctx context.Context) (SandboxOptions, error) {
	raw, err := s.GetSettings(ctx)
	if err != nil {
		return SandboxOptions{}, err
	}
	for _, key := range []string{"effective", "sandbox"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields[key] == nil {
			return SandboxOptions{}, fmt.Errorf("%w: missing or invalid get_settings field %s", ErrProtocol, key)
		}
		raw = fields[key]
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return SandboxOptions{}, fmt.Errorf("%w: invalid get_settings sandbox response", ErrProtocol)
	}
	var sandbox SandboxOptions
	for key, field := range map[string]**bool{
		"enabled":                  &sandbox.Enabled,
		"failIfUnavailable":        &sandbox.FailIfUnavailable,
		"allowUnsandboxedCommands": &sandbox.AllowUnsandboxedCommands,
	} {
		if value := fields[key]; value != nil {
			if err := json.Unmarshal(value, field); err != nil {
				return SandboxOptions{}, fmt.Errorf("%w: invalid get_settings sandbox field %s", ErrProtocol, key)
			}
		}
	}
	return sandbox, nil
}
