// Command mock runs an explicitly selected offline Codex app-server scenario.
package main

import (
	"context"
	"os"

	"github.com/zoumo/agent-sdk-go/codex/tools/mock"
)

func main() {
	os.Exit(mock.Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
