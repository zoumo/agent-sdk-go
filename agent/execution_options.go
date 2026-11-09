package agent

import "context"

// ExecutionOptions contains provider-native execution values for a main
// session. An empty field is omitted; nonempty values are passed literally.
type ExecutionOptions struct {
	Model           string
	ReasoningEffort string
}

// ExecutionValue separates the caller's last accepted request from an explicit
// native observation of the current value.
type ExecutionValue struct {
	Requested string
	Effective *string
}

// ExecutionOptionsReport is the complete known execution-options state for a
// session.
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

// ExecutionOptionsController updates one execution option at a time.
type ExecutionOptionsController interface {
	ExecutionOptionsReporter

	SetExecutionOption(
		ctx context.Context,
		change ExecutionOptionChange,
	) (ExecutionOptionsReceipt, error)
}
