package agent

import "strings"

// ToolKind is the closed, ACP-derived cross-agent action category for a tool.
// It is display and routing metadata, not a permission decision or evidence of
// behavioral equivalence between providers.
type ToolKind string

// ToolKind values classify a tool independently of its invocation arguments.
const (
	ToolKindRead       ToolKind = "read"
	ToolKindEdit       ToolKind = "edit"
	ToolKindDelete     ToolKind = "delete"
	ToolKindMove       ToolKind = "move"
	ToolKindSearch     ToolKind = "search"
	ToolKindExecute    ToolKind = "execute"
	ToolKindThink      ToolKind = "think"
	ToolKindFetch      ToolKind = "fetch"
	ToolKindSwitchMode ToolKind = "switch_mode"
	ToolKindOther      ToolKind = "other"
)

// ToolProvider identifies who provides a tool. Valid values are exactly
// ToolProviderAgent, ToolProviderDynamic, ToolProviderMCP, or a nonempty
// "mcp:<server>" suffix. The server suffix may contain additional colons.
type ToolProvider string

// Fixed ToolProvider values. A located MCP server is constructed as
// ToolProvider("mcp:" + server) rather than another constant.
const (
	ToolProviderAgent   ToolProvider = "agent"
	ToolProviderDynamic ToolProvider = "dynamic"
	ToolProviderMCP     ToolProvider = "mcp"
)

func validToolKind(kind ToolKind) bool {
	switch kind {
	case ToolKindRead,
		ToolKindEdit,
		ToolKindDelete,
		ToolKindMove,
		ToolKindSearch,
		ToolKindExecute,
		ToolKindThink,
		ToolKindFetch,
		ToolKindSwitchMode,
		ToolKindOther:
		return true
	default:
		return false
	}
}

func validToolProvider(provider ToolProvider) bool {
	switch provider {
	case ToolProviderAgent, ToolProviderDynamic, ToolProviderMCP:
		return true
	}
	value := string(provider)
	return strings.HasPrefix(value, "mcp:") && len(value) > len("mcp:")
}

func validToolIdentity(kind ToolKind, toolName string, provider ToolProvider) bool {
	return validToolKind(kind) && toolName != "" && validToolProvider(provider)
}
