// history-query-offline uses a synthetic subprocess, never an installed agent.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "--synthetic-peer" {
		err = peer()
	} else {
		err = run()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--synthetic-peer"}, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "history-example", Version: "synthetic"}}})
	if err != nil {
		return err
	}
	defer c.Close()
	// No Session/Open, resume, subscription or input. Filters are server-native;
	// the default scope is interactive/non-archived, not all historical threads.
	page, err := c.ListThreads(ctx, types.ThreadListParams{Limit: types.Some(uint32(1)), UseStateDbOnly: types.Some(true)})
	if err != nil {
		return err
	}
	if len(page.Data) != 1 {
		return fmt.Errorf("synthetic peer: expected one thread")
	}
	id := page.Data[0].ID
	// Explicitly fetch only the next page; the SDK never fetches it automatically.
	next, ok := page.NextCursor.Get()
	if !ok {
		return fmt.Errorf("synthetic peer: expected continuation cursor")
	}
	tail, err := c.ListThreads(ctx, types.ThreadListParams{Cursor: types.Some(next), Limit: types.Some(uint32(1)), UseStateDbOnly: types.Some(true)})
	if err != nil {
		return err
	}
	if !tail.NextCursor.IsNull() {
		return fmt.Errorf("synthetic peer: expected explicit page end")
	}
	fmt.Printf("threads: %s; next page: %d; end: true\n", id, len(tail.Data))
	metadata, err := c.ReadThread(ctx, types.ThreadReadParams{ThreadID: id, IncludeTurns: types.Some(false)})
	if err != nil {
		return err
	}
	fmt.Printf("metadata preview: %s\n", metadata.Thread.Preview)
	turns, err := c.ListThreadTurns(ctx, types.ThreadTurnsListParams{ThreadID: id, Limit: types.Some(uint32(25)), ItemsView: types.Some(types.TurnItemsViewSummary)})
	if err != nil {
		return err
	}
	if len(turns.Data) != 1 {
		return fmt.Errorf("synthetic peer: expected one turn")
	}
	fmt.Printf("summary: %s; items: %d\n", turns.Data[0].ID, len(turns.Data[0].Items))
	items, err := c.ListThreadItems(ctx, types.ThreadItemsListParams{ThreadID: id, TurnID: types.Some(turns.Data[0].ID), Limit: types.Some(uint32(25))})
	if err != nil {
		return err
	}
	if len(items.Data) != 1 || items.Data[0].Item.AgentMessage == nil {
		return fmt.Errorf("synthetic peer: expected saved message")
	}
	fmt.Printf("saved item: %s\n", items.Data[0].Item.AgentMessage.Text)
	// These saved values are not original delta/approval/hook events. Applications
	// retain native next/backwards cursors, unknown data and errors as needed.
	return c.Close()
}

func peer() error {
	const thread = `{"id":"saved","sessionId":"s","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/synthetic-server","ephemeral":false,"modelProvider":"synthetic","preview":"hello…","projectId":null,"source":"cli","status":{"type":"notLoaded"},"turns":[]}`
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params struct{ Cursor string }
		}
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return err
		}
		var result string
		switch m.Method {
		case "initialize":
			result = `{"userAgent":"synthetic/only","codexHome":"/synthetic-server","platformFamily":"unix","platformOs":"linux"}`
		case "initialized":
			continue
		case "thread/list":
			if m.Params.Cursor == "" {
				result = `{"data":[` + thread + `],"nextCursor":"opaque/+next","backwardsCursor":"opaque/back"}`
			} else {
				result = `{"data":[],"nextCursor":null}`
			}
		case "thread/read":
			result = `{"thread":` + thread + `}`
		case "thread/turns/list":
			result = `{"data":[{"id":"turn-1","items":[],"itemsView":"summary","status":"completed","error":null}],"nextCursor":null}`
		case "thread/items/list":
			result = `{"data":[{"turnId":"turn-1","item":{"type":"agentMessage","id":"item-1","text":"hello from saved content"}}],"nextCursor":null}`
		default:
			return fmt.Errorf("unexpected query operation %q", m.Method)
		}
		if _, err := fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", m.ID, result); err != nil {
			return err
		}
	}
	return scan.Err()
}
