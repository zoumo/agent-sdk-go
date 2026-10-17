// Observe one native turn on an explicitly chosen executable. This example may
// execute model work if run against an authenticated server; tests only compile it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/protocol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: observe-turn /explicit/path/to/codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := codex.NewStdio(ctx, codex.StdioOptions{
		Executable: os.Args[1], Args: []string{"app-server"},
		Initialize: protocol.InitializeParams{ClientInfo: protocol.ClientInfo{Name: "observe-turn", Version: "0.1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := client.StartThread(ctx, protocol.ThreadStartParams{
		Ephemeral:      protocol.Some(true),
		Sandbox:        protocol.Some(protocol.SandboxModeReadOnly),
		ApprovalPolicy: protocol.Some(protocol.AskForApproval{Value: protocol.Ptr(protocol.AskForApprovalValueNever)}),
	})
	if err != nil {
		return err
	}
	// Registration is active here. StartTurn may finish after completion arrives.
	events, err := client.Subscribe(ctx)
	if err != nil {
		return err
	}
	defer events.Close()
	started, err := client.StartTurn(ctx, protocol.TurnStartParams{
		ThreadID: thread.Thread.ID,
		Input:    []protocol.UserInput{{Text: protocol.Ptr(protocol.TextUserInput{Text: "Say hello briefly."})}},
	})
	if err != nil {
		return err
	}
	for {
		select {
		case <-events.Done():
			return subscriptionEnd(events)
		case event, ok := <-events.Events():
			if !ok {
				return subscriptionEnd(events)
			}
			// Loss status wins over a queued completion in this example.
			if err := events.Err(); err != nil {
				return err
			}
			if event.DecodeError != nil {
				return event.DecodeError
			}
			switch value := event.Data.(type) {
			case *protocol.AgentMessageDeltaNotification:
				if value.ThreadID == thread.Thread.ID && value.TurnID == started.Turn.ID {
					fmt.Print(value.Delta)
				}
			case *protocol.TurnCompletedNotification:
				if value.ThreadID == thread.Thread.ID && value.Turn.ID == started.Turn.ID {
					fmt.Printf("\nturn %s: %s\n", value.Turn.ID, value.Turn.Status)
					return events.Err()
				}
			}
			// Unknown methods and unknown native variants remain in event.Params.
		}
	}
	// An explicit interruption would be a separate client.InterruptTurn(ctx,
	// protocol.TurnInterruptParams{ThreadID: thread.Thread.ID, TurnID: started.Turn.ID}).
	// Its acknowledgement does not replace waiting for native turn/completed.
}
func subscriptionEnd(events *codex.Subscription) error {
	if err := events.Err(); err != nil {
		return err
	}
	return errors.New("notification stream ended before matching completion")
}
