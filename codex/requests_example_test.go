package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	codex "github.com/zoumo/agent-sdk-go/codex"
)

// answerFromTerminal is application code, never called on an SDK reader. It
// prints the native request and waits for an explicit caller-selected JSON result.
// In a real UI, watch Handle.Done as well and dismiss stale prompts.
func answerFromTerminal(ctx context.Context, request codex.Request) error {
	fmt.Printf("%s %s %s\n", request.ID(), request.Method(), request.Params())
	fmt.Println("Enter the native JSON result you choose for this request:")
	var result json.RawMessage
	if err := json.NewDecoder(os.Stdin).Decode(&result); err != nil {
		return err
	}
	return request.Handle().Answer(ctx, result)
}

// This example is compiled but has no Output directive, so tests do not run it.
func ExampleSession_Receive() {
	// Open a Session first. No request is approved automatically.
	consume := func(ctx context.Context, session *codex.Session) error {
		for {
			record, err := session.Receive(ctx)
			if err != nil {
				return err
			}
			if record.Request != nil && record.Request.Context().Err() == nil {
				if err := answerFromTerminal(ctx, record.Request.Request); err != nil {
					return err
				}
			}
			// Native events and observation evidence arrive through this same loop.
		}
	}
	_ = consume
}
