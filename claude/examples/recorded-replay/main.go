// SPDX-License-Identifier: Apache-2.0
// Replay a real CLI authentication failure. This never starts a real CLI/model.
package main

import (
	"context"
	"fmt"
	claude "github.com/zoumo/agent-sdk-go/claude"
	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
	"io"
	"os"
	"time"
)

func main() {
	var err error
	if os.Getenv(transcript.PeerEnvironment) == "1" {
		err = transcript.Peer(os.Stdin, os.Stdout)
	} else {
		err = run(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "recorded-replay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	client, err := claude.Open(ctx, claude.Options{CLIPath: exe, WorkDir: dir, Env: []string{"HOME=" + dir, transcript.PeerEnvironment + "=1", "GORACE=atexit_sleep_ms=0"}})
	if err != nil {
		return err
	}
	defer client.Close()
	prompt, err := transcript.Prompt()
	if err != nil {
		return err
	}
	if err = client.Send(ctx, prompt); err != nil {
		return err
	}
	fmt.Fprintln(out, "Offline replay: real CLI 2.1.258 authentication failure; no successful model/tool coverage.")
	result := false
	for {
		record, err := client.Receive(ctx)
		if err != nil {
			return err
		}
		native, ok := record.(claude.Event)
		if !ok {
			continue
		}
		if native.Result != nil {
			result = true
			fmt.Fprintf(out, "result: subtype=%s is_error=%t (subtype alone does not mean success)\n", native.Subtype, native.Result.IsError)
		}
		if native.System != nil && native.Subtype == "session_state_changed" && native.System.State != nil && *native.System.State == "idle" && result {
			fmt.Fprintln(out, "native idle after error result; explicit Close, no retry.")
			return client.Close()
		}
	}
}
