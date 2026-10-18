// This executable talks only to a synthetic copy of itself, never to Codex.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--fixture" {
		if err := fixture(); err != nil {
			os.Exit(1)
		}
		return
	}
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	client, err := codex.NewStdio(ctx, codex.StdioOptions{
		Executable: executable, Args: []string{"--fixture"}, Env: []string{},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "offline-subagents", Version: "1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	// Register before asking the fake peer to emit. PendingBytes forces the large
	// unknown-source envelope to be released explicitly, without timer ordering.
	live, err := client.Subscribe(ctx, "root", types.SubscriptionOptions{PendingBytes: 256})
	if err != nil {
		return err
	}
	defer live.Close()
	if err = client.Call(ctx, "fixture/emit", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(out, "live:")
	for {
		select {
		case record, ok := <-live.Events():
			if !ok {
				return fmt.Errorf("early close: %v", live.Err())
			}
			printRecord(out, record)
			if record.Method == "fixture/end" {
				if err = live.Close(); err != nil {
					return err
				}
				goto history
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
history:
	<-live.Done()
	if err = live.Err(); err != nil {
		return err
	}
	// Local observation close leaves the connection usable, without unsubscribe.
	if err = client.Call(ctx, "fixture/ping", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(out, "closed: err=nil; connection usable")
	seeded, err := client.Subscribe(ctx, "root", types.SubscriptionOptions{History: []json.RawMessage{
		json.RawMessage(`{"id":"child","parentThreadId":"root"}`),
		json.RawMessage(`{"id":"grand","parentThreadId":"child"}`),
	}})
	if err != nil {
		return err
	}
	if err = seeded.Close(); err != nil {
		return err
	}
	fmt.Fprintln(out, "history:")
	for record := range seeded.Events() {
		printRecord(out, record)
	}
	return seeded.Err()
}

func printRecord(out io.Writer, e types.Event) {
	switch d := e.Data.(type) {
	case *types.ObservationState:
		fmt.Fprintf(out, "ObservationState %s %s\n", d.State, d.Reason)
	case *types.SubagentCreated:
		fmt.Fprintf(out, "SubagentCreated %s parent=%s provenance=%s\n", d.ThreadID, d.ParentThreadID, d.Evidence.Provenance)
	case *types.SubagentAssociated:
		fmt.Fprintf(out, "SubagentAssociated %s parent=%s provenance=%s\n", d.ThreadID, d.ParentThreadID, d.Evidence.Provenance)
	default:
		var source struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(e.Params, &source)
		fmt.Fprintf(out, "%s thread=%s\n", e.Method, source.ThreadID)
	}
}

// Original synthetic native envelopes. fixture/* are private fake-peer methods,
// not Codex methods. Unexpected RPCs fail: the SDK must not resume/read children.
func fixture() error {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		reply := func(result string) { fmt.Fprintf(os.Stdout, "{\"id\":%s,\"result\":%s}\n", request.ID, result) }
		emit := func(method, params string) { fmt.Fprintf(os.Stdout, "{\"method\":%q,\"params\":%s}\n", method, params) }
		spawn := func(parent, child, item string) {
			emit("item/completed", fmt.Sprintf(`{"threadId":%q,"turnId":"turn","completedAtMs":1,"item":{"type":"collabAgentToolCall","id":%q,"agentsStates":{},"receiverThreadIds":[%q],"senderThreadId":%q,"status":"completed","tool":"spawnAgent","prompt":null,"model":null,"reasoningEffort":null}}`, parent, item, child, parent))
		}
		switch request.Method {
		case "initialize":
			reply(`{"userAgent":"synthetic/only","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`)
		case "initialized":
		case "fixture/emit":
			spawn("root", "child", "spawn-child")
			emit("item/agentMessage/delta", `{"threadId":"grand","turnId":"turn","itemId":"text","delta":"early"}`)
			spawn("child", "grand", "spawn-grand")
			emit("fixture/unknown", `{"threadId":"unrelated","opaque":"`+strings.Repeat("x", 512)+`"}`)
			emit("fixture/end", `{"threadId":"root"}`)
			reply(`{}`)
		case "fixture/ping":
			reply(`{}`)
		default:
			return fmt.Errorf("unexpected RPC %s", request.Method)
		}
	}
	return scanner.Err()
}
