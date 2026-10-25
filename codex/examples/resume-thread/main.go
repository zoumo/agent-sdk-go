// Command resume-thread explicitly opens a fresh connection, resumes a native ID
// already known to the caller, reads it, and detaches server-side observation.
// It is compiled by tests but never run. Running it launches the selected Codex
// executable, which may read configuration/credentials and perform side effects.
package main

import (
	"context"
	"fmt"
	"os"

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
	if len(os.Args) != 5 {
		return fmt.Errorf("usage: resume-thread /path/to/codex native-thread-id /absolute/cwd /absolute/codex-home")
	}
	ctx := context.Background()
	client, err := codex.NewStdio(ctx, codex.StdioOptions{
		Executable: os.Args[1], Args: []string{"app-server"},
		Env:        append(os.Environ(), "CODEX_HOME="+os.Args[4]),
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "resume-example", Version: "0.1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	response, err := client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: os.Args[2], Cwd: types.Some(os.Args[3]), ExcludeTurns: types.Some(true)})
	if err != nil {
		return err
	}
	fmt.Println("resumed", response.Thread.ID)
	var stored types.ThreadReadResponse
	if err := client.Call(ctx, "thread/read", types.ThreadReadParams{ThreadID: os.Args[2], IncludeTurns: types.Some(false)}, &stored); err != nil {
		return err
	}
	fmt.Println("stored", stored.Thread.ID)
	_, err = client.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: os.Args[2]})
	return err
}
