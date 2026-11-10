package agent

import (
	"context"
	"strings"
	"unicode/utf8"
)

// ExecutionOptions contains provider-native execution values for a main
// session. An empty field is omitted and is not a request to clear or restore a
// default. Nonempty values are passed literally without trimming, aliasing or
// cross-provider conversion. They apply to the main session and are intended
// for provider-determined later work. An update does not wait for idle or
// interrupt work already executing, whose native observation of the change is
// not guaranteed.
type ExecutionOptions struct {
	Model           string
	ReasoningEffort string
}

// Validate checks the provider-neutral execution-option values before native
// side effects. Empty values are valid because they omit the option.
func (o ExecutionOptions) Validate() error {
	if err := validateExecutionOptionEncoding(
		ExecutionOptionModel,
		o.Model,
	); err != nil {
		return err
	}
	return validateExecutionOptionEncoding(
		ExecutionOptionReasoningEffort,
		o.ReasoningEffort,
	)
}

func validateExecutionOptionEncoding(
	option ExecutionOption,
	value string,
) error {
	if value == "" {
		return nil
	}
	name := "execution option"
	switch option {
	case ExecutionOptionModel:
		name = "model"
	case ExecutionOptionReasoningEffort:
		name = "reasoning effort"
	}
	if !utf8.ValidString(value) {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonExecutionOptionInvalidUTF8,
			Message: name + " must be valid UTF-8",
		}
	}
	if strings.ContainsRune(value, '\x00') {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonExecutionOptionContainsNUL,
			Message: name + " must not contain NUL",
		}
	}
	return nil
}

// ExecutionValue separates the caller's last accepted request from an explicit
// native observation of the current value.
type ExecutionValue struct {
	Requested string
	Effective *string
}

// ExecutionOptionsReport is the complete known execution-options state for a
// session. A successful update can advance Requested, clear invalidated
// Effective evidence, and still leave Effective unknown; callers must not treat
// acceptance as proof of current behavior.
type ExecutionOptionsReport struct {
	Model           ExecutionValue
	ReasoningEffort ExecutionValue
}

// ExecutionOptionsReporter is implemented only by sessions that can report
// their known execution-options state.
type ExecutionOptionsReporter interface {
	EffectiveExecutionOptions() ExecutionOptionsReport
}

// ExecutionOption identifies one provider-neutral runtime execution option.
type ExecutionOption string

const (
	ExecutionOptionModel           ExecutionOption = "model"
	ExecutionOptionReasoningEffort ExecutionOption = "reasoningEffort"
)

// ExecutionOptionChange requests one literal value update for one option.
type ExecutionOptionChange struct {
	Option ExecutionOption
	Value  string
}

// ExecutionOptionsReceipt reports the requested change, observed acceptance
// stage, and the complete execution-options state after the operation.
type ExecutionOptionsReceipt struct {
	Change     ExecutionOptionChange
	Acceptance Acceptance
	Report     ExecutionOptionsReport
}

// ExecutionOptionsController updates one execution option at a time. Updates
// are accepted requests for provider-determined later work; they do not wait
// for idle, interrupt active work, restart the process, retry, or configure
// child sessions. Native behavior determines whether work already executing
// observes the change.
type ExecutionOptionsController interface {
	ExecutionOptionsReporter

	SetExecutionOption(
		ctx context.Context,
		change ExecutionOptionChange,
	) (ExecutionOptionsReceipt, error)
}
