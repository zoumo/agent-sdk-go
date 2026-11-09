// SPDX-License-Identifier: Apache-2.0

package types

import (
	"encoding/json"
	"time"
)

// InitializeOptions selects native initialization fields. A nil Options.Initialize
// requests forwardSubagentText=true; an explicit value preserves omission of that
// field. Hooks and agent definitions retain their native JSON representation.
type InitializeOptions struct {
	Hooks               json.RawMessage `json:"hooks"`
	Agents              json.RawMessage `json:"agents,omitempty"`
	ForwardSubagentText *bool           `json:"forwardSubagentText,omitempty"`
}

// Limits bounds protocol and control resources. Zero fields select defaults. A bound exceeded
// by incoming traffic terminates the connection explicitly; frames are not
// silently discarded and the stdout reader never waits for an event consumer.
// Queue/count limits must be at most 65,536, except PermissionHistory (1,048,576). FrameBytes is at most 64 MiB and StderrBytes
// at most 16 MiB. Invalid configuration is rejected before starting a process.
type Limits struct {
	// EventBuffer values outside 0..65,536 are rejected before process startup.

	// Deprecated: ignored; Receive backlog grows dynamically.
	EventBuffer     int
	FrameBytes      int
	PendingControls int
	ControlBuffer   int
	StderrBytes     int
	// PendingPermissions bounds unanswered incoming controls, including retained
	// permission handles.
	PendingPermissions int
	// PermissionHistory bounds distinct incoming control/cancellation IDs per client.
	// IDs are never evicted, so a retired request cannot acquire a new responder.
	PermissionHistory int
}

// MCPTransport selects one native Claude MCP transport. The zero value is
// invalid; callers must choose a transport explicitly.
type MCPTransport string

const (
	MCPTransportStdio MCPTransport = "stdio"
	MCPTransportHTTP  MCPTransport = "http"
	MCPTransportSSE   MCPTransport = "sse"
)

// MCPServer declares one external MCP server for a Session. Name is the unique
// native configuration key and is not sent as an external server field.
type MCPServer struct {
	Name      string
	Transport MCPTransport
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

// Options is the callback-free configuration for a local CLI process.
// Use claude.OptionsFromTypes to adapt it to runtime options. Env and MCP server
// slices/maps retain normal Go sharing.
// Zero values select the client defaults; validation occurs at Open.
type Options struct {
	CLIPath string
	WorkDir string
	Model   string
	// Effort passes the native creation-only --effort value literally. Empty omits it.
	Effort string
	// AppendSystemPrompt appends text after the CLI's own system instructions
	// through one native --append-system-prompt=<text> argument and does not
	// replace them. Empty omits the option. The text must be valid UTF-8 and
	// contain no NUL.
	AppendSystemPrompt string
	// SDKMCPServers names application-hosted native SDK MCP servers.
	SDKMCPServers []string
	// MCPServers declares external stdio, HTTP or SSE MCP servers. The SDK
	// copies these values when opening the Session.
	MCPServers []MCPServer
	// StrictMCPConfig adds --strict-mcp-config, asking the CLI to ignore MCP
	// servers from other settings, only when true.
	StrictMCPConfig bool
	Thinking        *ThinkingConfig
	Initialize      *InitializeOptions
	// ResumeID asks a fresh CLI process to resume its native session. It is
	// passed literally; the SDK does not replay messages or restore pending work.
	ResumeID string
	// Tools is passed as a comma-separated native --tools allowlist. Nil or
	// empty omits the option; one empty string disables ordinary built-in tools.
	Tools []string
	// DisallowedTools is passed as a comma-separated native --disallowedTools
	// option. Nil or empty omits the option.
	DisallowedTools   []string
	Env               []string
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	Limits            Limits
}

// ThinkingConfig is creation-only native thinking configuration from SDK
// 0.3.258. Enabled without BudgetTokens and adaptive both launch adaptive.
// Disabled accepts neither budget nor display. Budget is deprecated and can
// act as an on/off switch on some models, rather than a hard token ceiling.
type ThinkingConfig struct {
	Type         string
	BudgetTokens *int
	Display      *string
}
