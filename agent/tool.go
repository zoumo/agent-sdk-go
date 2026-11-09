package agent

import "strings"

type ToolKind string

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

type ToolProvider string

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
