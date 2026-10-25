// SPDX-License-Identifier: Apache-2.0
// Explicit manual recorder entry point. Default tests never invoke real agents.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/zoumo/agent-sdk-go/claude/tools/recorder"
)

func run() error { return recorder.Run() }

func main() {
	if err := run(); err != nil {
		if errors.Is(err, recorder.ErrPolicyIsolation) {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, "recording stopped; raw diagnostics withheld")
		}
		os.Exit(1)
	}
}
