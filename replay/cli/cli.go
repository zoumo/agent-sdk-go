// Package cli runs the agent-replay command.
package cli

import (
	"context"
	"os"

	"github.com/zoumo/agent-sdk-go/replay/internal/command"
)

// Execute runs one invocation and returns its process exit code.
func Execute(ctx context.Context, args []string, stdin, stdout, stderr *os.File) int {
	return command.Execute(ctx, args, command.IO{In: stdin, Out: stdout, Err: stderr})
}
