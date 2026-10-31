// Package protocol owns recording, strict native replay and transport bridging.
// It has no case selection, driver assertions or SDK dependencies.
package protocol

import (
	"context"

	"github.com/zoumo/agent-sdk-go/tools/agent-sdk-testify/protocol/command"
)

// Execute runs a protocol operation using caller-owned cancellation and streams.
func Execute(ctx context.Context, args []string, streams command.IO) int {
	return command.Execute(ctx, args, command.Handlers{Record: runRecord, Mock: runMock, Connect: runConnect}, streams)
}
