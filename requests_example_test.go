package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	codex "github.com/zoumo/codex-sdk-go"
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
func ExampleClient_Requests() {
	// Obtain client from NewStdio and start/resume work on another goroutine.
	// No request is approved automatically, even for recognized methods.
	consume := func(ctx context.Context, client *codex.Client) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case request, ok := <-client.Requests():
				if !ok {
					return client.Err()
				}
				if request.Handle().Err() != nil {
					continue
				}
				if err := answerFromTerminal(ctx, request); err != nil {
					return err
				}
			}
		}
	}
	_ = consume
}
