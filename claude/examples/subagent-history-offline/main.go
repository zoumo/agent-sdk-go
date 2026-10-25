// Browse synthetic child saved content without a Session, CLI or model.
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
	config, err := os.MkdirTemp("", "claude-child-query-example-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(config)
	const parent = "11111111-1111-4111-8111-111111111111"
	project := filepath.Join(config, "projects", "-synthetic-example")
	children := filepath.Join(project, parent, "subagents", "workflows", "demo")
	if err = os.MkdirAll(children, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(project, parent+".jsonl"), nil, 0600); err != nil {
		return err
	}
	source := `{"type":"user","uuid":"u","parentUuid":null,"message":{"content":"Synthetic child question"}}` + "\n" + `{"type":"assistant","uuid":"a","parentUuid":"u","message":{"content":[{"type":"text","text":"Synthetic child answer"}]}}` + "\n"
	if err = os.WriteFile(filepath.Join(children, "agent-worker.jsonl"), []byte(source), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(children, "agent-worker.meta.json"), []byte(`{"toolUseId":"synthetic-parent-tool","parentAgentId":null}`), 0600); err != nil {
		return err
	}
	scope := claude.SessionQueryScope{ConfigDir: config}
	listed, err := claude.ListSubagents(context.Background(), parent, claude.ListSubagentsOptions{Scope: scope})
	if err != nil {
		return err
	}
	for _, info := range listed.Records {
		fmt.Printf("child=%s running=%s parent-known=%v\n", info.AgentID, info.Running, info.ParentAgentID != nil)
		// Offset/Limit slice visible messages after the entire bounded read. There
		// is no cursor/total/snapshot token; separate pages can observe later writes.
		page, err := claude.GetSubagentMessages(context.Background(), parent, info.AgentID, claude.SubagentMessagesOptions{Scope: scope, Limit: 10})
		if err != nil {
			return err
		}
		for _, message := range page.Messages {
			fmt.Printf("%s %s: %s\n", message.Type, message.UUID, message.Message)
		}
		fmt.Printf("coverage=%s atomic=%v sidecar=%s\n", page.Coverage.Status, page.Coverage.Atomic, page.Coverage.SidecarStatus)
	}
	return nil
}
