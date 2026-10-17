// SPDX-License-Identifier: Apache-2.0
package claude_test

import (
	"context"
	"fmt"
	"log"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
)

// This example compiles during tests but is not executed. Running it requires
// your own installed Claude Code CLI and its configured account or provider.
func ExampleOpen() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := claude.Open(ctx, claude.Options{CLIPath: "claude"})
	if err != nil {
		log.Print(err)
		return
	}
	defer client.Close()
	if err := client.Send(ctx, "Explain what this directory contains."); err != nil {
		log.Print(err)
		return
	}
	for {
		event, err := client.Receive(ctx)
		if err != nil {
			log.Print(err)
			return
		}
		if event.Assistant != nil {
			for _, block := range event.Assistant.Content.Blocks {
				if block.Text != nil {
					fmt.Println(block.Text.Text)
				}
			}
		}
		if event.Result != nil {
			return
		} // Explicit defer closes the client; Result does not.
	}
}
