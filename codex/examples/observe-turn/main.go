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
	session, err := codex.Open(ctx, codex.SessionOptions{Client: client, Thread: types.ThreadStartParams{
		Ephemeral: types.Some(true), Sandbox: types.Some(types.SandboxModeReadOnly), ApprovalPolicy: types.Some(types.AskForApproval{Value: types.Ptr(types.AskForApprovalValueNever)}),
	}})
	if err != nil {
		return err
	}
	defer session.Abort()
	submitted, err := session.Submit(ctx, codex.Input{Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: "Say hello briefly."})}}})
	if err != nil {
		return err
	}
	fmt.Println("native turn", submitted.Response.Turn.ID)
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if record.Request != nil {
			return fmt.Errorf("unexpected request %s requires an application decision", record.Request.Method())
		}
		if record.Observation != nil {
			fmt.Println("observation", record.Observation.Reason)
			continue
		}
		if record.Event == nil {
			continue
		}
		if record.Event.DecodeError != nil {
			return record.Event.DecodeError
		}
		switch value := record.Event.Data.(type) {
		case *types.AgentMessageDeltaNotification:
			fmt.Printf("[%s/%s/%s] %s", value.ThreadID, value.TurnID, value.ItemID, value.Delta)
		case *types.TurnCompletedNotification:
			fmt.Printf("\nthread %s turn %s: %s\n", value.ThreadID, value.Turn.ID, value.Turn.Status)
		}
		// A result does not close Session. Interrupt uses an explicit native TurnID.
	}
}
