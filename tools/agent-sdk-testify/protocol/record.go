package protocol

import (
	"context"
	"github.com/zoumo/agent-sdk-go/tools/agent-sdk-testify/protocol/capture"
	"github.com/zoumo/agent-sdk-go/tools/agent-sdk-testify/protocol/command"
)

func runRecord(ctx context.Context, options command.RecordOptions, streams command.IO) error {
	return capture.Run(ctx, options, streams)
}
