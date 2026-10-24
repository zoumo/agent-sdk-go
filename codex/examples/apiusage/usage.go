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

// Observe consumes one Session. The application supplies all request decisions.
func Observe(ctx context.Context, session *codex.Session, prompt string) error {
	if _, err := session.Submit(ctx, codex.Input{Input: []types.UserInput{{Text: types.Ptr(types.TextUserInput{Text: prompt})}}}); err != nil {
		return err
	}
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if record.Request != nil {
			return fmt.Errorf("request %s requires an application decision", record.Request.Method())
		}
		if record.Observation != nil {
			fmt.Println(record.Observation.Reason)
			continue
		}
		if record.Event == nil {
			continue
		}
		if record.Event.DecodeError != nil {
			return record.Event.DecodeError
		}
		if data, ok := record.Event.Data.(*types.AgentMessageDeltaNotification); ok {
			fmt.Printf("[%s] %s", data.ThreadID, data.Delta)
		}
	}
}

// Reply keeps native question IDs and command policy extensions on the original
// request. RPC rejection is distinct from the human denial decision.
func Reply(ctx context.Context, request *codex.IncomingRequest, command types.CommandExecutionRequestApprovalResponse, answers types.ToolRequestUserInputResponse) error {
	data, err := request.Data()
	if err != nil {
		return err
	}
	switch data.(type) {
	case *types.CommandExecutionRequestApprovalParams:
		return request.Handle().Answer(ctx, command)
	case *types.ToolRequestUserInputParams:
		return request.Answer(ctx, answers)
	default:
		return request.Handle().Reject(ctx, &codex.RPCError{Code: -32601, Message: "unsupported request"})
	}
}
func Close(session *codex.Session) error { return session.Close() }
