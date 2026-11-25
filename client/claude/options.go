// SPDX-License-Identifier: Apache-2.0

package claude

import "github.com/zoumo/agent-sdk-go/claude/types"

// OptionsFromTypes adapts callback-free configuration to runtime options.
// SessionID and ResumeID are mutually exclusive.
// Set runtime-only Subagents, ChildDetailPolicy, HandleSDKMCP, SessionStartProbe,
// and MCPFiles on the returned value when needed.
// Env and MCP server slices/maps are shared, as when copying Options; this
// function does not validate them.
func OptionsFromTypes(config types.Options) Options {
	return Options{
		CLIPath:                         config.CLIPath,
		WorkDir:                         config.WorkDir,
		Model:                           config.Model,
		Effort:                          config.Effort,
		AppendSystemPrompt:              config.AppendSystemPrompt,
		SDKMCPServers:                   config.SDKMCPServers,
		MCPServers:                      config.MCPServers,
		StrictMCPConfig:                 config.StrictMCPConfig,
		ReceiveElicitation:              config.ReceiveElicitation,
		Thinking:                        config.Thinking,
		Initialize:                      config.Initialize,
		SessionID:                       config.SessionID,
		ClaimSession:                    config.ClaimSession,
		ResumeID:                        config.ResumeID,
		PermissionMode:                  config.PermissionMode,
		AllowDangerouslySkipPermissions: config.AllowDangerouslySkipPermissions,
		Sandbox:                         config.Sandbox,
		Tools:                           config.Tools,
		DisallowedTools:                 config.DisallowedTools,
		Env:                             config.Env,
		InitializeTimeout:               config.InitializeTimeout,
		ShutdownTimeout:                 config.ShutdownTimeout,
		Limits:                          config.Limits,
	}
}

// Types returns the process configuration. ChildDetailPolicy, Subagents,
// HandleSDKMCP, SessionStartProbe, and MCPFiles stay with runtime Options and are not
// round-tripped.
// Env and MCP server slices/maps retain their backing storage.
func (o Options) Types() types.Options {
	return types.Options{
		CLIPath:                         o.CLIPath,
		WorkDir:                         o.WorkDir,
		Model:                           o.Model,
		Effort:                          o.Effort,
		AppendSystemPrompt:              o.AppendSystemPrompt,
		SDKMCPServers:                   o.SDKMCPServers,
		MCPServers:                      o.MCPServers,
		StrictMCPConfig:                 o.StrictMCPConfig,
		ReceiveElicitation:              o.ReceiveElicitation,
		Thinking:                        o.Thinking,
		Initialize:                      o.Initialize,
		SessionID:                       o.SessionID,
		ClaimSession:                    o.ClaimSession,
		ResumeID:                        o.ResumeID,
		PermissionMode:                  o.PermissionMode,
		AllowDangerouslySkipPermissions: o.AllowDangerouslySkipPermissions,
		Sandbox:                         o.Sandbox,
		Tools:                           o.Tools,
		DisallowedTools:                 o.DisallowedTools,
		Env:                             o.Env,
		InitializeTimeout:               o.InitializeTimeout,
		ShutdownTimeout:                 o.ShutdownTimeout,
		Limits:                          o.Limits,
	}
}
