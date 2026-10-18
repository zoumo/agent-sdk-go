// Package apiusage contains compile-checked examples. Nothing is run by tests.
package apiusage

import (
	"context"
	"fmt"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func Configure(ctx context.Context, executable string) (*codex.Client, error) {
	return codex.NewStdio(ctx, codex.StdioOptions{
		Executable: executable, Args: []string{"app-server"},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "example", Version: "1"}},
	})
}

// Observe creates a thread when id is empty, or resumes that native ID. The
// caller owns the client and independently services Requests while this runs.
func Observe(ctx context.Context, client *codex.Client, id, prompt string) error {
	if id == "" {
		started, err := client.StartThread(ctx, types.ThreadStartParams{})
		if err != nil {
			return err
		}
		id = started.Thread.ID
	} else {
		resumed, err := client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: id})
		if err != nil {
			return err
		}
		id = resumed.Thread.ID
	}
	sub, err := client.Subscribe(ctx, id, types.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer sub.Close()
	_, err = client.StartTurn(ctx, types.TurnStartParams{
		ThreadID: id, Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}},
	})
	if err != nil {
		return err
	}
	for event := range sub.Events() {
		if event.DecodeError != nil {
			return event.DecodeError
		}
		switch data := event.Data.(type) {
		case *types.AgentMessageDeltaNotification:
			fmt.Printf("[%s] %s", data.ThreadID, data.Delta)
		case *types.SubagentCreated:
			fmt.Println("created", data.ThreadID, data.ParentThreadID, data.Evidence)
		case *types.SubagentAssociated:
			fmt.Println("associated", data.ThreadID, data.ParentThreadID)
		case *types.ObservationState:
			fmt.Println(data.State, data.Reason, data.UnresolvedThreadID)
		}
		// Root turn/completed does not imply child or background work has stopped.
	}
	return sub.Err()
}

// Reply uses caller-selected native response shapes and the original handle.
// The UI supplies the decision or answers; this function does not choose them.
func Reply(ctx context.Context, request codex.Request,
	command types.CommandExecutionRequestApprovalResponse,
	answers types.ToolRequestUserInputResponse) error {
	data, err := request.Data()
	if err != nil {
		return err
	}
	switch data.(type) {
	case *types.CommandExecutionRequestApprovalParams:
		return request.Handle().Answer(ctx, command)
	case *types.ToolRequestUserInputParams:
		return request.Handle().Answer(ctx, answers)
	default:
		return request.Handle().Reject(ctx, &codex.RPCError{Code: -32601, Message: "unsupported request"})
	}
}

// Close ends local client ownership. Subscription.Close only ends that local
// observation. UnsubscribeThread is an explicit native detachment RPC.
func Close(client *codex.Client) error { return client.Close() }
