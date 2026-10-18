// Observe a managed root and descendants on an explicitly chosen executable. This example may
// execute model work if run against an authenticated server; tests only compile it.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
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
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "observe-turn", Version: "0.1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	thread, err := client.StartThread(ctx, types.ThreadStartParams{
		Ephemeral:      types.Some(true),
		Sandbox:        types.Some(types.SandboxModeReadOnly),
		ApprovalPolicy: types.Some(types.AskForApproval{Value: types.Ptr(types.AskForApprovalValueNever)}),
	})
	if err != nil {
		return err
	}
	// Registration is active here. StartTurn may finish after completion arrives.
	events, err := client.Subscribe(ctx, thread.Thread.ID, types.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer events.Close()
	_, err = client.StartTurn(ctx, types.TurnStartParams{
		ThreadID: thread.Thread.ID,
		Input:    []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "Say hello briefly."})}},
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
			native := event
			if native.DecodeError != nil {
				return native.DecodeError
			}
			switch value := native.Data.(type) {
			case *types.AgentMessageDeltaNotification:
				fmt.Printf("[%s/%s/%s] %s", value.ThreadID, value.TurnID, value.ItemID, value.Delta)
			case *types.SubagentCreated:
				fmt.Printf("child %s created by %s\n", value.ThreadID, value.ParentThreadID)
			case *types.SubagentAssociated:
				fmt.Printf("child %s associated with %s\n", value.ThreadID, value.ParentThreadID)
			case *types.ObservationState:
				fmt.Printf("observation %s: %s\n", value.State, value.Reason)
			case *types.TurnCompletedNotification:
				fmt.Printf("\nthread %s turn %s: %s\n", value.ThreadID, value.Turn.ID, value.Turn.Status)
				// Keep observing descendants and background work until the caller context ends.
			}
			// Unknown methods and unknown native variants remain in native.Params.
		}
	}
	// An explicit interruption would be a separate client.InterruptTurn(ctx,
	// types.TurnInterruptParams{ThreadID: thread.Thread.ID, TurnID: started.Turn.ID}).
	// Its acknowledgement does not replace waiting for native turn/completed.
}
func subscriptionEnd(events *codex.Subscription) error {
	if err := events.Err(); err != nil {
		return err
	}
	return nil
}
