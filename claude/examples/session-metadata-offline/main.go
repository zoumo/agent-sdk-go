// This example discovers synthetic saved sessions without a Claude runtime.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/claude"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	config, err := os.MkdirTemp("", "claude-query-example-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(config)
	const session = "11111111-1111-4111-8111-111111111111"
	dir := filepath.Join(config, "projects", "-synthetic-example")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	source := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"11111111-1111-4111-8111-111111111111","message":{"content":"Synthetic saved question"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"11111111-1111-4111-8111-111111111111","message":{"content":[{"type":"text","text":"Synthetic saved answer"}]}}` + "\n"
	if err = os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte(source), 0400); err != nil {
		return err
	}
	ctx := context.Background()
	scope := claude.SessionQueryScope{ConfigDir: config}
	listed, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 10})
	if err != nil {
		return err
	}
	for _, saved := range listed.Sessions {
		info, err := claude.GetSessionInfo(ctx, saved.SessionID, claude.SessionInfoOptions{Scope: scope})
		if err != nil {
			return err
		}
		if info.Info == nil {
			continue
		}
		fmt.Printf("session=%s preview=%q running=%s sampled=%v\n", saved.SessionID, info.Info.Summary, info.Info.Running, info.Info.Sampled)
		// Offset slices visible messages after native chain reconstruction.
		// Every call rereads the bounded source; these are not I/O pages or a
		// shared snapshot. This example owns its immutable synthetic source.
		for offset := 0; ; {
			page, err := claude.GetSessionMessages(ctx, saved.SessionID, claude.SessionMessagesOptions{Scope: scope, Offset: offset, Limit: 1})
			if err != nil {
				return err
			}
			if len(page.Messages) == 0 {
				fmt.Println("end: empty content slice")
				break
			}
			for _, m := range page.Messages {
				fmt.Printf("%s %s: %s\n", m.Type, m.UUID, m.Message)
			}
			fmt.Printf("coverage=%s atomic=%v\n", page.Coverage.Status, page.Coverage.Atomic)
			offset += len(page.Messages)
		}
	}
	return nil
}
