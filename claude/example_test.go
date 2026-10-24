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
func ExampleOpen() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client, err := Open(ctx, SessionOptions{CLIPath: "claude"})
	if err != nil {
		log.Print(err)
		return
	}
	defer client.Abort()
	if _, err := client.Submit(ctx, Input{Text: "Explain what this directory contains."}); err != nil {
		log.Print(err)
		return
	}
	for {
		record, err := client.Receive(ctx)
		if err != nil {
			log.Print(err)
			return
		}
		event := record.Event
		if event == nil {
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

// This example compiles without running a CLI. The fixed native baseline cannot
// confirm recovery without execution; Load reports ErrResumeUnconfirmed.
func ExampleLoad() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	session, err := Load(ctx, LoadOptions{Options: Options{CLIPath: "claude"}, History: HistoryOptions{SessionID: "previous-native-session-id", CWD: "/absolute/project", ConfigDir: "/absolute/config"}})
	if err != nil {
		log.Print(err)
		return
	}
	defer session.Close()
	for {
		received, err := session.Receive(ctx)
		if err != nil {
			log.Print(err)
			return
		}
		if received.Load != nil {
			fmt.Println(received.Load.Status, received.Load.Err)
			return
		}
	}
}

// The application obtains its decision before answering the actual request.
// This example is compile-only and does not run a CLI or choose an approval.
func ExampleIncomingRequest_Deny() {
	consume := func(ctx context.Context, session *Session, userDecision func(*IncomingRequest) DenialOptions) error {
		for {
			record, err := session.Receive(ctx)
			if err != nil {
				return err
			}
			if record.Request != nil {
				if err := record.Request.Deny(ctx, userDecision(record.Request)); err != nil {
					return err
				}
			}
		}
	}
	_ = consume
}
