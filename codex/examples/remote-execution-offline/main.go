// SPDX-License-Identifier: Apache-2.0

// This example demonstrates a best-effort remote MCP profile for Codex. It runs
// only a synthetic app-server and never contacts the configured MCP endpoint.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/mcp"
)

const (
	peerEnvironment  = "CODEX_GO_REMOTE_EXECUTION_PEER"
	traceEnvironment = "CODEX_GO_REMOTE_EXECUTION_TRACE"
	startResult      = `{"thread":{"id":"remote-thread","sessionId":"synthetic","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/synthetic","ephemeral":true,"modelProvider":"synthetic","preview":"","projectId":null,"source":"appServer","status":{"type":"idle"},"turns":[]},"approvalPolicy":"never","approvalsReviewer":"user","cwd":"/synthetic","model":"synthetic","modelProvider":"synthetic","sandbox":{"type":"readOnly"}}`
)

func main() {
	var err error
	if os.Getenv(peerEnvironment) == "1" {
		err = peer()
	} else {
		err = run(os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir, err := os.MkdirTemp("", "codex-remote-execution-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	codexHome := filepath.Join(dir, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	client, err := codex.NewStdio(ctx, codex.StdioOptions{
		Executable: executable,
		Args:       []string{"app-server"},
		Env: []string{
			"HOME=" + dir,
			"CODEX_HOME=" + codexHome,
			peerEnvironment + "=1",
			traceEnvironment + "=" + tracePath,
			"GORACE=atexit_sleep_ms=0",
		},
		Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "remote-execution-offline", Version: "1"}},
	})
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := codex.Open(ctx, codex.SessionOptions{
		Client: client,
		Thread: types.ThreadStartParams{Ephemeral: types.Some(true)},
		Features: map[codex.Feature]bool{
			codex.FeatureShellTool:       false,
			codex.FeatureViewImage:       false,
			codex.FeatureImageGeneration: false,
		},
		MCPServers: []mcp.Server{{
			Name:       "remote",
			Transport:  mcp.TransportHTTP,
			URL:        "https://example.invalid/mcp",
			EnvHeaders: map[string]string{"Authorization": "REMOTE_AUTHORIZATION"},
		}},
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Abort() }()
	if err := session.Close(); err != nil {
		return err
	}
	if err := client.Close(); err != nil {
		return err
	}

	methods, params, err := readAppServerTrace(tracePath)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "methods=%s\n", strings.Join(methods, ",")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "thread_start_params=%s\n", params); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "boundary=apply_patch may remain and local zero execution is not guaranteed"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "isolation=ambient MCP config may merge without an isolated CODEX_HOME or outer sandbox; use external sandbox/permission policy for stronger isolation"); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "evidence=synthetic app-server params only; no Codex model or remote MCP was contacted")
	return err
}

func readAppServerTrace(path string) ([]string, json.RawMessage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	var methods []string
	var startParams json.RawMessage
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var frame struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return nil, nil, err
		}
		methods = append(methods, frame.Method)
		if frame.Method == "thread/start" {
			startParams = append(json.RawMessage(nil), frame.Params...)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(startParams) == 0 {
		return nil, nil, errors.New("synthetic app-server did not receive thread/start")
	}
	return methods, startParams, nil
}

// peer records app-server frames and completes only the calls needed by Open and Close.
func peer() error {
	tracePath := os.Getenv(traceEnvironment)
	if tracePath == "" {
		return errors.New("missing synthetic trace path")
	}
	transcript, err := os.Create(tracePath)
	if err != nil {
		return err
	}
	defer transcript.Close()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		if _, err := transcript.Write(append(raw, '\n')); err != nil {
			return err
		}
		if err := transcript.Sync(); err != nil {
			return err
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return err
		}
		var result string
		switch request.Method {
		case "initialize":
			result = fmt.Sprintf(`{"userAgent":"synthetic/only","codexHome":%q,"platformFamily":"unix","platformOs":"linux"}`, os.Getenv("CODEX_HOME"))
		case "initialized":
			continue
		case "thread/start":
			result = startResult
		case "thread/unsubscribe":
			result = `{"status":"unsubscribed"}`
		default:
			return fmt.Errorf("unexpected RPC %q", request.Method)
		}
		if _, err := fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", request.ID, result); err != nil {
			return err
		}
	}
	return scanner.Err()
}
