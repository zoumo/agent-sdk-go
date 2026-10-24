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

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
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
	session, err := codex.Open(ctx, codex.SessionOptions{Client: client})
	if err != nil {
		return err
	}
	defer session.Abort()
	if err = client.Call(ctx, "fixture/emit", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(out, "live:")
	for {
		record, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if record.Event != nil {
			fmt.Fprintf(out, "%s thread=%s\n", record.Event.Method, record.Source.ThreadID)
		}
		if record.Observation != nil {
			fmt.Fprintf(out, "observation %s thread=%s parent=%s\n", record.Observation.Reason, record.Observation.ThreadID, record.Observation.ParentThreadID)
		}
		if record.Event != nil && record.Event.Method == "fixture/end" {
			break
		}
	}
	snapshot := session.Unattributed()
	fmt.Fprintf(out, "quarantine: records=%d coverage-gap=%t; earlier facts are not replayed\n", len(snapshot.Records), snapshot.CoverageGap)
	if err := session.Close(); err != nil {
		return err
	}
	if err := client.Call(ctx, "fixture/ping", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(out, "closed: err=nil; shared connection usable")
	return nil
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
		case "thread/start":
			reply(`{"thread":{"id":"root","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}`)
		case "thread/unsubscribe":
			reply(`{"status":"unsubscribed"}`)
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
