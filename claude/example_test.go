// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"fmt"
	"log"
	"time"
)

// This example compiles during tests but is not executed. Running it requires
// your own installed Claude Code CLI and its configured account or provider.
func ExampleNewClient() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := NewClient(ctx, Options{CLIPath: "claude"})
	if err != nil {
		log.Print(err)
		return
	}
	defer client.Abort()
	if err := client.Send(ctx, "Explain what this directory contains."); err != nil {
		log.Print(err)
		return
	}
	for {
		record, err := client.Receive(ctx)
		if err != nil {
			log.Print(err)
			return
		}
		event, ok := record.(Event)
		if !ok {
			// Handle relationship and observation records in the application UI.
			continue
		}
		if event.Assistant != nil {
			for _, block := range event.Assistant.Content.Blocks {
				if block.Text != nil {
					fmt.Println(block.Text.Text)
				}
			}
		}
		if event.ParentToolUseID == nil && event.Result != nil {
			return
		} // This application stops at the root result; background work may still run.
	}
}

// This example compiles but does not run. The session must already exist in
// your installed CLI's native storage; the SDK does not replay its history.
func ExampleClient_RequestedResumeID() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := NewClient(ctx, Options{CLIPath: "claude", ResumeID: "previous-native-session-id"})
	if err != nil {
		log.Print(err)
		return
	}
	defer client.Abort()
	fmt.Println("Requested:", client.RequestedResumeID())
	if id, observed := client.SessionID(); observed {
		fmt.Println("Observed:", id)
	}
	if err := client.Interrupt(ctx); err != nil {
		log.Print(err)
	}
	// Acknowledgment does not imply rollback. Receive still delivers turn results.
}

// This example is compiled but never executed by tests. A real run requires a
// configured CLI and can incur account usage. The application owns the human UI.
func ExamplePermissionRequest_Answer() {
	requests := make(chan *PermissionRequest, 8)
	client, err := NewClient(context.Background(), Options{
		CLIPath: "claude",
		PermissionHandler: func(request *PermissionRequest) {
			select {
			case requests <- request: // Retain the handle for the application UI.
			case <-request.Context().Done():
			}
		},
	})
	if err != nil {
		log.Print(err)
		return
	}
	defer client.Abort()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := client.Send(ctx, "Read the requested project file."); err != nil {
		log.Print(err)
		return
	}
	// A real application concurrently drains Receive and displays requests.
	select {
	case request := <-requests:
		fmt.Println(request.RequestID(), request.ToolName(), string(request.Input()))
		if err := request.Answer(ctx, PermissionAnswer{
			Behavior: PermissionDeny, Message: "The user declined this request",
		}); err != nil {
			log.Print(err)
		}
	case <-client.Done():
		log.Print(client.Err())
	case <-ctx.Done():
	}
}
