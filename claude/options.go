// SPDX-License-Identifier: Apache-2.0

package claude

import "github.com/zoumo/agent-sdk-go/claude/types"

// OptionsFromTypes adapts callback-free configuration to runtime options.
// Set runtime-only Subagents, ChildDetailPolicy and HandleSDKMCP on the returned value when needed.
// Env and MCP server slices/maps are shared, as when copying Options; this
// function does not validate them.
func OptionsFromTypes(config types.Options) Options {
	return Options{
		CLIPath:            config.CLIPath,
		WorkDir:            config.WorkDir,
		Model:              config.Model,
		Effort:             config.Effort,
		AppendSystemPrompt: config.AppendSystemPrompt,
		SDKMCPServers:      config.SDKMCPServers,
		MCPServers:         config.MCPServers,
		StrictMCPConfig:    config.StrictMCPConfig,
		Thinking:           config.Thinking,
		Initialize:         config.Initialize,
		ResumeID:           config.ResumeID,
		Tools:              config.Tools,
		DisallowedTools:    config.DisallowedTools,
		Env:                config.Env,
		InitializeTimeout:  config.InitializeTimeout,
		ShutdownTimeout:    config.ShutdownTimeout,
		Limits:             config.Limits,
	}
}

// Types returns the process configuration. ChildDetailPolicy, Subagents and HandleSDKMCP stay with
// runtime Options and are not round-tripped.
// Env and MCP server slices/maps retain their backing storage.
func (o Options) Types() types.Options {
	return types.Options{
		CLIPath:            o.CLIPath,
		WorkDir:            o.WorkDir,
		Model:              o.Model,
		Effort:             o.Effort,
		AppendSystemPrompt: o.AppendSystemPrompt,
		SDKMCPServers:      o.SDKMCPServers,
		MCPServers:         o.MCPServers,
		StrictMCPConfig:    o.StrictMCPConfig,
		Thinking:           o.Thinking,
		Initialize:         o.Initialize,
		ResumeID:           o.ResumeID,
		Tools:              o.Tools,
		DisallowedTools:    o.DisallowedTools,
		Env:                o.Env,
		InitializeTimeout:  o.InitializeTimeout,
		ShutdownTimeout:    o.ShutdownTimeout,
		Limits:             o.Limits,
	}
}
