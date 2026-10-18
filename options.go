// SPDX-License-Identifier: Apache-2.0

package claude

import "github.com/zoumo/claude-agent-sdk-go/types"

// OptionsFromTypes adapts callback-free configuration to runtime options.
// Set PermissionHandler on the returned value when needed. Env is shared,
// as it is when copying Options directly; this function does not validate it.
func OptionsFromTypes(config types.Options) Options {
	return Options{
		CLIPath:           config.CLIPath,
		WorkDir:           config.WorkDir,
		Model:             config.Model,
		ResumeID:          config.ResumeID,
		Env:               config.Env,
		InitializeTimeout: config.InitializeTimeout,
		ShutdownTimeout:   config.ShutdownTimeout,
		Limits:            config.Limits,
	}
}

// Types returns the callback-free part of Options. The handler stays with the
// runtime options; Env retains the same backing slice.
func (o Options) Types() types.Options {
	return types.Options{
		CLIPath:           o.CLIPath,
		WorkDir:           o.WorkDir,
		Model:             o.Model,
		ResumeID:          o.ResumeID,
		Env:               o.Env,
		InitializeTimeout: o.InitializeTimeout,
		ShutdownTimeout:   o.ShutdownTimeout,
		Limits:            o.Limits,
	}
}
