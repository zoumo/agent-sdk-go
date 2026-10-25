// This example creates synthetic saved content and queries it without Claude.
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
	page, err := claude.GetSessionMessages(context.Background(), session, claude.SessionMessagesOptions{
		Scope: claude.SessionQueryScope{ConfigDir: config}, Limit: 10,
	})
	if err != nil {
		return err
	}
	for _, m := range page.Messages {
		fmt.Printf("%s %s: %s\n", m.Type, m.UUID, m.Message)
	}
	fmt.Printf("coverage=%s atomic=%v\n", page.Coverage.Status, page.Coverage.Atomic)
	return nil
}
