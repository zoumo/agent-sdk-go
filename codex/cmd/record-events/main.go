// Explicit manual recorder entry point. Default tests never invoke real agents.
package main

import (
	"fmt"
	"os"

	"github.com/zoumo/agent-sdk-go/codex/tools/recorder"
)

func run() error { return recorder.Run() }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
