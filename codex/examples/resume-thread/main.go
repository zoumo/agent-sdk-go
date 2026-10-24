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
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "resume-example", Version: "0.1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := codex.Load(ctx, codex.LoadOptions{Client: client, HistoryOptions: codex.HistoryOptions{ThreadID: os.Args[2], CWD: os.Args[3], CodexHome: os.Args[4]}})
	if err != nil {
		return err
	}
	defer session.Close()
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if record.Event != nil {
			fmt.Println(record.Origin, record.Event.Method)
		}
		if record.Request != nil {
			return fmt.Errorf("request %s requires an application decision", record.Request.Method())
		}
		if record.Load != nil {
			if record.Load.Err != nil {
				return record.Load.Err
			}
			fmt.Println(record.Load.Status)
			return session.Close()
		}
	}
}
