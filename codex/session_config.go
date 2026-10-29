package codex

import (
	"context"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

var ErrUnsupportedConfiguration = errors.New("codex: thread settings require experimentalApi initialization")

// ConfigurationReceipt separates the requested settings and native RPC
// acceptance. Response preserves the native empty acknowledgement and unknown
// fields. It does not report observed settings or a current-turn switch.
// Observe thread/settings/updated independently through Receive.
type ConfigurationReceipt struct {
	Requested      types.ThreadSettingsUpdateParams
	Response       types.ThreadSettingsUpdateResponse
	NativeAccepted bool
}

// ThinkingChange preserves native effort and reasoning-summary fields. Omitted and null effort both
// leave the native override unchanged at the pinned protocol version; null is
// not a request to clear an effort setting.
type ThinkingChange struct {
	Effort types.Optional[types.ReasoningEffort]
	// Summary is the native reasoning-summary display setting, independent of effort.
	Summary types.Optional[types.ReasoningSummary]
}

// SetThinking requests native reasoning effort for subsequent main-thread turns.
func (s *Session) SetThinking(ctx context.Context, change ThinkingChange) (ConfigurationReceipt, error) {
	return s.configure(ctx, types.ThreadSettingsUpdateParams{ThreadID: s.root, Effort: change.Effort, Summary: change.Summary})
}

// SetModel requests a native model identifier for subsequent turns of the main
// thread. It neither waits for idle nor changes any child thread explicitly.
func (s *Session) SetModel(ctx context.Context, model string) (ConfigurationReceipt, error) {
	return s.configure(ctx, types.ThreadSettingsUpdateParams{ThreadID: s.root, Model: types.Some(model)})
}

func (s *Session) configure(ctx context.Context, requested types.ThreadSettingsUpdateParams) (ConfigurationReceipt, error) {
	r := ConfigurationReceipt{Requested: requested}
	var invalid error
	if !s.client.experimentalAPI {
		invalid = ErrUnsupportedConfiguration
	} else if model, ok := requested.Model.Get(); ok && model == "" {
		invalid = ErrInvalidInput
	} else if summary, ok := requested.Summary.Get(); ok && summary == "" {
		invalid = ErrInvalidInput
	} else if effort, ok := requested.Effort.Get(); ok && effort == "" {
		invalid = ErrInvalidInput
	}
	if invalid != nil {
		return r, &CallError{Method: "thread/settings/update", Dispatch: NotSent, Cause: invalid}
	}
	err := s.client.callOwned(ctx, "thread/settings/update", requested, &r.Response, false, nil, s)
	r.NativeAccepted = err == nil
	return r, err
}
