package agent

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
