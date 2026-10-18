// SPDX-License-Identifier: Apache-2.0
// Explicit manual recorder entry point. Default tests never invoke real agents.
package main

import (
	"fmt"
	"os"

	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func run() error { return recorder.Run() }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "recording stopped; inspect private status (no raw diagnostics printed)")
		os.Exit(1)
	}
}
