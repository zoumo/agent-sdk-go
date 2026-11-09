package agent

import (
	"context"
	"unicode/utf8"
)

// ExecutionOptionsRuntime supplies one adapter session's native
// execution-option boundary. It is adapter support, not a requirement for
// ordinary Session implementations.
type ExecutionOptionsRuntime interface {
	ExecutionOptionsReporter

	// Lock serializes updates with every other execution-option update for the
	// same session, including the legacy model entry point.
	Lock()
	Unlock()
	// RequireOpen rejects a closed session before any native write.
	RequireOpen() error
	// Apply writes one change to the native session.
	Apply(context.Context, ExecutionOptionChange) error
	// ConvertError maps a native error into the unified error contract.
	ConvertError(error) error
	// PrepareAccepted returns the state commit to run after native success. It
	// runs while the update lock is still held.
	PrepareAccepted(ExecutionOptionChange) func()
	// ClearUncertain removes effective values invalidated by an unknown outcome.
	ClearUncertain(ExecutionOption)
	// Acceptance describes the dispatch stages observed for a native error.
	Acceptance(error) Acceptance
}

// SetExecutionOption validates and applies one runtime execution-option change.
// Adapters call this from ExecutionOptionsController.SetExecutionOption.
func SetExecutionOption(
	ctx context.Context,
	change ExecutionOptionChange,
	runtime ExecutionOptionsRuntime,
) (ExecutionOptionsReceipt, error) {
	if err := validateExecutionOptionChange(change); err != nil {
		return executionOptionsReceipt(
			change,
			executionOptionsNotSent(),
			runtime.EffectiveExecutionOptions(),
		), err
	}

	runtime.Lock()
	defer runtime.Unlock()

	if err := runtime.RequireOpen(); err != nil {
		return executionOptionsReceipt(
			change,
			executionOptionsNotSent(),
			runtime.EffectiveExecutionOptions(),
		), err
	}
	commit := runtime.PrepareAccepted(change)
	if err := runtime.Apply(ctx, change); err != nil {
		converted := runtime.ConvertError(err)
		if CodeOf(converted) == ErrorOutcomeUnknown {
			runtime.ClearUncertain(change.Option)
		}
		return executionOptionsReceipt(
			change,
			runtime.Acceptance(err),
			runtime.EffectiveExecutionOptions(),
		), converted
	}
	commit()
	return executionOptionsReceipt(
		change,
		executionOptionsAccepted(),
		runtime.EffectiveExecutionOptions(),
	), nil
}

// SetExecutionModel implements the legacy single-model update on top of
// SetExecutionOption.
func SetExecutionModel(
	ctx context.Context,
	model string,
	runtime ExecutionOptionsRuntime,
) (ConfigurationReceipt, error) {
	if model == "" {
		return ConfigurationReceipt{}, &Error{
			Code:    ErrorInvalidArgument,
			Reason:  "missing_model",
			Message: "model must be nonempty",
		}
	}
	receipt, err := SetExecutionOption(ctx, ExecutionOptionChange{
		Option: ExecutionOptionModel,
		Value:  model,
	}, runtime)
	if err != nil {
		return ConfigurationReceipt{}, err
	}
	return ConfigurationReceipt{
		Model:      model,
		Acceptance: receipt.Acceptance,
	}, nil
}

func validateExecutionOptionChange(change ExecutionOptionChange) error {
	switch change.Option {
	case ExecutionOptionModel, ExecutionOptionReasoningEffort:
	default:
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  "execution_option_unknown",
			Message: "execution option is not supported",
		}
	}
	if change.Value == "" {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  "execution_option_empty_value",
			Message: "execution option value must be nonempty",
		}
	}
	if !utf8.ValidString(change.Value) {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  "execution_option_invalid_utf8",
			Message: "execution option value must be valid UTF-8",
		}
	}
	return nil
}

func executionOptionsAccepted() Acceptance {
	sent, accepted := true, true
	return Acceptance{Sent: &sent, NativeAccepted: &accepted}
}

func executionOptionsNotSent() Acceptance {
	sent := false
	return Acceptance{Sent: &sent}
}

func executionOptionsReceipt(
	change ExecutionOptionChange,
	acceptance Acceptance,
	report ExecutionOptionsReport,
) ExecutionOptionsReceipt {
	return ExecutionOptionsReceipt{
		Change:     change,
		Acceptance: acceptance,
		Report:     report,
	}
}
