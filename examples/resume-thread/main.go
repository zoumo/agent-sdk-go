// Command resume-thread explicitly opens a fresh connection, resumes a native ID
// already known to the caller, reads it, and detaches server-side observation.
// It is compiled by tests but never run. Running it launches the selected Codex
// executable, which may read configuration/credentials and perform side effects.
package main

import (
	"context"
	"fmt"
	"os"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: resume-thread /path/to/codex native-thread-id")
	}
	ctx := context.Background()
	client, err := codex.NewStdio(ctx, codex.StdioOptions{
		Executable: os.Args[1], Args: []string{"app-server"},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "resume-example", Version: "0.1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	resumed, err := client.ResumeThread(ctx, types.ThreadResumeParams{ThreadID: os.Args[2]})
	if err != nil {
		return err
	}
	read, err := client.ReadThread(ctx, types.ThreadReadParams{ThreadID: resumed.Thread.ID, IncludeTurns: types.Some(true)})
	if err != nil {
		return err
	}
	fmt.Println(read.Thread.ID)
	detached, err := client.UnsubscribeThread(ctx, types.ThreadUnsubscribeParams{ThreadID: resumed.Thread.ID})
	if err != nil {
		return err
	}
	fmt.Println(detached.Status)
	return client.Close()
}
