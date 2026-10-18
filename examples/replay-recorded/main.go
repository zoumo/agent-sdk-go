// replay-recorded runs only its embedded sanitized transcript peer, never Codex.
package main

import (
	"context"
	"fmt"
	"github.com/zoumo/codex-sdk-go/internal/recording"
	"os"
	"time"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--peer" {
		f, e := recording.Load(os.Args[2])
		if e == nil {
			e = recording.Peer(os.Stdin, os.Stdout, f)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	executable, e := os.Executable()
	if e == nil {
		for _, s := range []string{"s1", "s2"} {
			if e = recording.Replay(ctx, executable, s, os.Stdout); e != nil {
				break
			}
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
