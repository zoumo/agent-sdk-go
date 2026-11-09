package agent

import "path/filepath"

// OpenOptions contains provider-neutral creation options. An adapter that
// cannot enforce a nonzero option rejects it before native side effects.
// Provider-specific creation options belong on the concrete adapter.
type OpenOptions struct {
	Cwd            string
	SystemPrompt   string
	AdditionalDirs []string
	Features       Features
	// MCPServers is the complete final external MCP list for this operation. The
	// caller composes Runtime defaults with Session declarations before calling a
	// provider, which does not merge them again. Resume callers must repeat the
	// complete list because the SDK does not persist it.
	MCPServers []MCPServer

	Execution ExecutionOptions
}

// Validate checks the provider-neutral creation options before native side
// effects. Provider-specific support is checked by the adapter.
func (o OpenOptions) Validate() error {
	if o.Cwd == "" {
		return invalidArgument("missing_cwd", "working directory is required")
	}
	if !filepath.IsAbs(o.Cwd) {
		return invalidArgument("cwd_not_absolute", "working directory must be absolute")
	}
	return validateMCPServers(o.MCPServers)
}

// ResumeOptions identifies an existing native main session to resume.
type ResumeOptions struct {
	SessionID string
	OpenOptions
}

// Validate checks the provider-neutral resume options before native side
// effects.
func (o ResumeOptions) Validate() error {
	if o.SessionID == "" {
		return invalidArgument("missing_session_id", "session ID is required")
	}
	return o.OpenOptions.Validate()
}

// Features contains provider-neutral runtime feature requests. A nil field
// uses the provider default.
type Features struct {
	AgentQuestions *bool
}

// AgentQuestionsEnabled reports the requested value, applying the current
// default of true when the request omits the field.
func (f Features) AgentQuestionsEnabled() bool {
	return f.AgentQuestions == nil || *f.AgentQuestions
}

// EffectiveFeatures reports the requested features that the adapter can
// actually enforce for a session.
type EffectiveFeatures struct {
	AgentQuestions bool
}
