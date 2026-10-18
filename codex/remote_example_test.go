package codex_test

import (
	"context"
	"fmt"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// This example is compiled, not executed; the endpoint and token are placeholders.
func ExampleNewRemote() {
	ctx := context.Background()
	client, err := codex.NewRemote(ctx, codex.RemoteOptions{
		URL:         "wss://codex.example.com/",
		BearerToken: "caller-supplied-token",
		Initialize:  types.InitializeParams{ClientInfo: types.ClientInfo{Name: "my-client", Version: "0.1"}},
	})
	if err != nil {
		return
	}
	defer client.Close()
	started, err := client.StartThread(ctx, types.ThreadStartParams{Ephemeral: types.Some(true)})
	if err != nil {
		return
	}
	fmt.Println(started.Thread.ID)
}
