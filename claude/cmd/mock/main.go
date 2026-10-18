// SPDX-License-Identifier: Apache-2.0
// Command mock is an offline, scripted Claude Code stream-json protocol peer.
package main

import (
	"context"
	"os"

	"github.com/zoumo/agent-sdk-go/claude/tools/mock"
)

func main() {
	os.Exit(mock.CLI(context.Background(), os.Args[1:], os.Getenv(mock.ScenarioEnvironment), os.Stdin, os.Stdout, os.Stderr))
}
