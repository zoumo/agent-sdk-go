package agentclaude

import (
	"bytes"
	"slices"
	"time"

	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

// Config fixes the local Claude CLI process and its initialization. Session
// working directories and resume identities come from the unified operation.
type Config struct {
	CLIPath string
	Env     []string
	// ConfigDir is the explicit absolute Claude local-storage root used by
	// Resume preflight and postcheck. It is not inferred from HOME or Cwd.
	ConfigDir         string
	Initialize        *claudetypes.InitializeOptions
	ChildDetailPolicy claude.ChildDetailPolicy
	Subagents         claude.SubagentOptions
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	Limits            claude.Limits
}

func cloneConfig(config Config) Config {
	cloned := config
	cloned.Env = slices.Clone(config.Env)
	cloned.Subagents.History = slices.Clone(config.Subagents.History)
	if config.Initialize != nil {
		initialize := *config.Initialize
		initialize.Hooks = bytes.Clone(config.Initialize.Hooks)
		initialize.Agents = bytes.Clone(config.Initialize.Agents)
		if config.Initialize.ForwardSubagentText != nil {
			value := *config.Initialize.ForwardSubagentText
			initialize.ForwardSubagentText = &value
		}
		cloned.Initialize = &initialize
	}
	return cloned
}
