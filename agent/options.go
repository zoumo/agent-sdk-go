package agent

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// OpenOptions contains provider-neutral creation options. An adapter that
// cannot enforce a nonzero option rejects it before native side effects.
// Provider-specific creation options belong on the concrete adapter.
type OpenOptions struct {
	Cwd string
	// AdditionalSystemPrompt appends caller text after provider defaults for
	// this Open or Resume operation. Empty omits provider-native prompt
	// configuration. See harness/modules/unified-agent/design.md#additional-system-prompt
	// for the complete contract.
	AdditionalSystemPrompt string
	AdditionalDirs         []string
	Features               Features
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
	if !utf8.ValidString(o.AdditionalSystemPrompt) {
		return invalidArgument(
			"additional_system_prompt_invalid_utf8",
			"additional system prompt must be valid UTF-8",
		)
	}
	if strings.ContainsRune(o.AdditionalSystemPrompt, '\x00') {
		return invalidArgument(
			"additional_system_prompt_contains_nul",
			"additional system prompt must not contain NUL",
		)
	}
	if err := validateMCPServers(o.MCPServers); err != nil {
		return err
	}
	return o.Execution.Validate()
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
