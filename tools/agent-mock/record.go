package main

import (
	"context"
	"github.com/zoumo/agent-sdk-go/tools/agent-mock/capture"
	"github.com/zoumo/agent-sdk-go/tools/agent-mock/command"
)

func runRecord(ctx context.Context, options command.RecordOptions, streams command.IO) error {
	return capture.Run(ctx, options, streams)
}
