package main

import (
	"context"
	"github.com/zoumo/agent-sdk-go/tools/agent-mock/command"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := command.Execute(ctx, os.Args[1:], command.Handlers{Record: runRecord, Mock: runMock, Connect: runConnect}, command.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	cancel()
	os.Exit(code)
}
