// Command agent-replay owns environments and process orchestration, never SDK assertions.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoumo/agent-sdk-go/replay/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
