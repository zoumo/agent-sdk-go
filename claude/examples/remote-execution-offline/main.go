// SPDX-License-Identifier: Apache-2.0

// This example demonstrates how Claude can retain external MCP tools while
// disabling ordinary built-in tools. It runs only a synthetic launcher.
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
	"time"

	claude "github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/mcp"
)

const (
	peerEnvironment    = "CLAUDE_GO_REMOTE_EXECUTION_PEER"
	captureEnvironment = "CLAUDE_GO_REMOTE_EXECUTION_CAPTURE"
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

	dir, err := os.MkdirTemp("", "claude-remote-execution-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	capture := filepath.Join(dir, "argv.json")
	session, err := claude.Open(ctx, claude.SessionOptions{
		CLIPath: executable,
		WorkDir: dir,
		Env: []string{
			"HOME=" + dir,
			peerEnvironment + "=1",
			captureEnvironment + "=" + capture,
			"GORACE=atexit_sleep_ms=0",
		},
		Tools: []string{""},
		MCPServers: []mcp.Server{{
			Name:      "remote",
			Transport: mcp.TransportHTTP,
			URL:       "https://example.invalid/mcp",
			Headers:   map[string]string{"Authorization": "Bearer ${REMOTE_TOKEN}"},
		}},
		StrictMCPConfig: true,
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Abort() }()

	data, err := os.ReadFile(capture)
	if err != nil {
		return err
	}
	var captured struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		return err
	}
	argv, err := json.Marshal(captured.Argv)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "argv=%s\n", argv); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "boundary=EndConversation may remain when MCP tools are present; --tools does not deny MCP tools"); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, "evidence=synthetic launcher argv only; no Claude model or remote MCP was contacted")
	return err
}

// peer records the exact argv used by the SDK and completes only initialize.
func peer() error {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		_, err := fmt.Fprintln(os.Stdout, claude.MinimumCLIVersion+" (Claude Code)")
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var initialize struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &initialize); err != nil {
		return err
	}
	if initialize.Type != "control_request" || initialize.Request.Subtype != "initialize" || initialize.RequestID == "" {
		return errors.New("expected initialize control request")
	}
	data, err := json.Marshal(map[string]any{"argv": os.Args[1:]})
	if err != nil {
		return err
	}
	if err := os.WriteFile(os.Getenv(captureEnvironment), data, 0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": initialize.RequestID,
			"response":   map[string]any{},
		},
	}); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, reader)
	return err
}
