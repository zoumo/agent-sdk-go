package agentclaude

import (
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
)

const claudeMCPToolPrefix = "mcp__"

type claudeToolIdentity struct {
	kind     agent.ToolKind
	toolName string
	provider agent.ToolProvider
}

func identifyClaudeTool(toolName string) claudeToolIdentity {
	identity := claudeToolIdentity{
		kind:     agent.ToolKindOther,
		toolName: toolName,
		provider: agent.ToolProviderAgent,
	}
	if !strings.HasPrefix(toolName, claudeMCPToolPrefix) {
		identity.kind = claudeToolKind(toolName)
		return identity
	}

	identity.provider = agent.ToolProviderMCP
	remainder := strings.TrimPrefix(toolName, claudeMCPToolPrefix)
	separator := strings.Index(remainder, "__")
	if separator <= 0 ||
		separator+len("__") >= len(remainder) ||
		strings.Count(remainder, "__") != 1 ||
		strings.Contains(remainder, "___") {
		return identity
	}
	identity.provider = agent.ToolProvider(
		string(agent.ToolProviderMCP) + ":" + remainder[:separator],
	)
	return identity
}

func claudeToolKind(toolName string) agent.ToolKind {
	switch toolName {
	case "Read":
		return agent.ToolKindRead
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return agent.ToolKindEdit
	case "Bash":
		return agent.ToolKindExecute
	case "Glob", "Grep", "WebSearch":
		return agent.ToolKindSearch
	case "WebFetch":
		return agent.ToolKindFetch
	case "TodoWrite", "TaskCreate", "TaskUpdate":
		return agent.ToolKindThink
	case "ExitPlanMode":
		return agent.ToolKindSwitchMode
	default:
		return agent.ToolKindOther
	}
}
