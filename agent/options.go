package agent

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// OpenOptions contains adapter-neutral creation options. An adapter that
// cannot enforce a nonzero option rejects it before native side effects.
// Adapter-specific creation options belong on the concrete adapter.
type OpenOptions struct {
	Cwd string
	// ProcessEnv is an additive, set-only overlay for a Claude Session CLI.
	// Nil or empty leaves the provider environment unchanged; empty values set
	// variables to empty strings. Other adapters reject nonempty declarations.
	ProcessEnv map[string]string
	// ShellEnv adds variables to Codex shell/unified-exec environments. It does
	// not change the app-server environment. Claude rejects nonempty declarations.
	ShellEnv map[string]string
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

	Config SessionConfig
}

// Validate checks the adapter-neutral creation options before native side
// effects. Adapter-specific support is checked by the adapter.
func (o OpenOptions) Validate() error {
	if o.Cwd == "" {
		return invalidArgument("missing_cwd", "working directory is required")
	}
	if !filepath.IsAbs(o.Cwd) {
		return invalidArgument("cwd_not_absolute", "working directory must be absolute")
	}
	if !utf8.ValidString(o.AdditionalSystemPrompt) {
		return invalidArgument(
			ErrorReasonAdditionalSystemPromptInvalidUTF8,
			"additional system prompt must be valid UTF-8",
		)
	}
	if strings.ContainsRune(o.AdditionalSystemPrompt, '\x00') {
		return invalidArgument(
			ErrorReasonAdditionalSystemPromptContainsNUL,
			"additional system prompt must not contain NUL",
		)
	}
	for _, scope := range []struct {
		name string
		env  map[string]string
	}{
		{"ProcessEnv", o.ProcessEnv}, {"ShellEnv", o.ShellEnv},
	} {
		if err := validateEnvironment(scope.env); err != nil {
			return invalidArgument(ErrorReasonEnvironmentInvalid, scope.name+": "+err.Error())
		}
	}
	if err := validateMCPServers(o.MCPServers); err != nil {
		return err
	}
	return o.Config.Validate()
}

// ResumeOptions identifies an existing native main session to resume.
type ResumeOptions struct {
	SessionID string
	OpenOptions
}

// Validate checks the adapter-neutral resume options before native side
// effects.
func (o ResumeOptions) Validate() error {
	if o.SessionID == "" {
		return invalidArgument("missing_session_id", "session ID is required")
	}
	return o.OpenOptions.Validate()
}

// Features contains adapter-neutral runtime feature requests. A nil field
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
