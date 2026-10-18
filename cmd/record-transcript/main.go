// SPDX-License-Identifier: Apache-2.0
// Explicit manual recorder. Never called by tests, examples, or CI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	claude "github.com/zoumo/claude-agent-sdk-go"
	"github.com/zoumo/claude-agent-sdk-go/internal/transcript"
)

const s1 = "Read only numbers.txt (two lines: 7 and 11), then use Write once to create summary.txt containing exactly total=18 followed by a newline. Use no other tools or files. Give one short final sentence."
const s2 = "Use AskUserQuestion exactly once with the question 'Which synthetic color?' and exactly two options Blue and Green. Wait for the answer, then give one short final sentence. Do not use other tools."

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "recording stopped; inspect private status (no raw diagnostics printed)")
		os.Exit(1)
	}
}
func run() error {
	if os.Getenv("TRANSCRIPT_RECORD_PEER") == "1" {
		return proxy()
	}
	mode := flag.String("mode", "", "record or e2e")
	story := flag.String("story", "", "s1 or s2")
	cli := flag.String("cli", "", "absolute approved CLI")
	ledger := flag.String("session-ledger", "", "absolute durable ledger directory shared by this recording session")
	out := flag.String("private-dir", "", "new private capture directory")
	flag.Parse()
	if err := transcript.LiveGuard(*mode, os.Getenv("SDK_REAL_AGENT_E2E"), os.Getenv("CI"), *cli); err != nil {
		return err
	}
	if (*story != "s1" && *story != "s2") || !filepath.IsAbs(*out) || !filepath.IsAbs(*ledger) {
		return fmt.Errorf("invalid story/private directory")
	}
	// Version probe is read-only; no account or credential queries.
	version, err := exec.Command(*cli, "--version").Output()
	if err != nil || string(version) != "2.1.258 (Claude Code)\n" {
		return fmt.Errorf("unapproved CLI version")
	}
	if err := os.MkdirAll(*ledger, 0700); err != nil {
		return err
	}
	attempt, err := os.OpenFile(filepath.Join(*ledger, *story+".attempt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("story already attempted or ledger unavailable")
	}
	attempt.Close()
	if err := os.Mkdir(*out, 0700); err != nil {
		return err
	} // existing capture refuses retry
	project := filepath.Join(*out, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "numbers.txt"), []byte("7\n11\n"), 0600); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := []string{"SDK_REAL_AGENT_E2E=1", "TRANSCRIPT_MODE=" + *mode, "TRANSCRIPT_RECORD_PEER=1", "TRANSCRIPT_CLI=" + *cli, "TRANSCRIPT_PRIVATE=" + *out, "TRANSCRIPT_STORY=" + *story, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1"}
	for _, k := range []string{"HOME", "PATH", "TMPDIR", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	result := false
	modelError := false
	idle := false
	callbacks := 0
	allowed := 0
	prompt := s1
	if *story == "s2" {
		prompt = s2
	}
	observations, err := os.OpenFile(filepath.Join(*out, "observations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer observations.Close()
	var handlerMu sync.Mutex
	client, openErr := claude.Open(ctx, claude.Options{CLIPath: exe, WorkDir: project, Env: env, InitializeTimeout: 30 * time.Second, ShutdownTimeout: 3 * time.Second, Limits: claude.Limits{FrameBytes: 2 << 20, StderrBytes: 256 << 10, PermissionWorkers: 1}, PermissionHandler: func(p *claude.PermissionRequest) {
		handlerMu.Lock()
		defer handlerMu.Unlock()
		callbacks++
		answer := claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "outside exact synthetic story policy"}
		var input map[string]any
		if json.Unmarshal(p.Input(), &input) == nil {
			path, _ := input["file_path"].(string)
			if *story == "s1" && allowed < 2 {
				read := p.ToolName() == "Read" && transcript.SafeFile(project, path, "numbers.txt")
				content, _ := input["content"].(string)
				write := p.ToolName() == "Write" && transcript.SafeFile(project, path, "summary.txt") && content == "total=18\n"
				if read || write {
					allowed++
					answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow}
				}
			}
			if *story == "s2" && allowed == 0 && p.ToolName() == "AskUserQuestion" {
				qs, ok := input["questions"].([]any)
				if ok && len(qs) == 1 {
					q, _ := qs[0].(map[string]any)
					if q["question"] == "Which synthetic color?" && exactOptions(q["options"]) {
						input["answers"] = map[string]string{"Which synthetic color?": "Blue"}
						updated, _ := json.Marshal(input)
						allowed++
						answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}
					}
				}
			}
		}
		answerCtx, stop := context.WithTimeout(p.Context(), 15*time.Second)
		defer stop()
		_ = p.Answer(answerCtx, answer)
	}})
	runErr := openErr
	if openErr == nil {
		runErr = client.Send(ctx, prompt)
		for runErr == nil {
			record, e := client.Receive(ctx)
			if e != nil {
				runErr = e
				break
			}
			if e = json.NewEncoder(observations).Encode(record); e != nil {
				runErr = e
				break
			}
			if native, ok := record.(claude.Event); ok {
				if native.Type == "result" {
					result = true
					if native.Result != nil {
						modelError = native.Result.IsError
					}
				}
				if native.System != nil && native.Subtype == "session_state_changed" && native.System.State != nil && *native.System.State == "idle" {
					idle = true
					if result {
						break
					}
				}
			}
		}
		if *story == "s1" && runErr == nil {
			data, e := os.ReadFile(filepath.Join(project, "summary.txt"))
			if e != nil || string(data) != "total=18\n" || allowed != 2 {
				runErr = fmt.Errorf("S1 tool/file evidence incomplete")
			}
		}
		if *story == "s2" && runErr == nil && allowed != 1 {
			runErr = fmt.Errorf("S2 question evidence incomplete")
		}
		if modelError && runErr == nil {
			runErr = fmt.Errorf("native error result")
		}
		closeErr := client.Close()
		if closeErr != nil {
			runErr = closeErr
		}
	}
	handlerMu.Lock()
	defer handlerMu.Unlock()
	summary := map[string]any{"story": *story, "seconds": time.Since(start).Seconds(), "root_attempts": 1, "children": 0, "permission_callbacks": callbacks, "allowed": allowed, "result_observed": result, "idle_observed": idle, "error": runErr != nil, "prompt": prompt, "sdk_base": "282f85aed93c571d4aaa500751d6786b021897b6"}
	b, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(*out, "status.json"), b, 0600); err != nil {
		return err
	}
	fmt.Printf("%s: result=%t idle=%t callbacks=%d allowed=%d seconds=%.2f\n", *story, result, idle, callbacks, allowed, time.Since(start).Seconds())
	return runErr
}
func proxy() error {
	if err := transcript.LiveGuard(os.Getenv("TRANSCRIPT_MODE"), os.Getenv("SDK_REAL_AGENT_E2E"), os.Getenv("CI"), os.Getenv("TRANSCRIPT_CLI")); err != nil {
		return err
	}
	if os.Getenv("TRANSCRIPT_STORY") != "s1" && os.Getenv("TRANSCRIPT_STORY") != "s2" {
		return fmt.Errorf("invalid proxy story")
	}
	dir := os.Getenv("TRANSCRIPT_PRIVATE")
	story := os.Getenv("TRANSCRIPT_STORY")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	tools := "Read,Write"
	ask := "Read"
	if story == "s2" {
		tools = "AskUserQuestion"
		ask = "AskUserQuestion"
	}
	settings := `{"disableAllHooks":true,"permissions":{"ask":["` + ask + `","Write"]}}`
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "--print", "--restricted", "--safe-mode", "--no-session-persistence", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-chrome", "--setting-sources", "", "--settings", settings, "--tools", tools, "--max-budget-usd", "1", "--system-prompt", "You perform only the provided synthetic fixture task. No external context, extra tools, or extra turns. Be concise.")
	cmd := exec.CommandContext(ctx, os.Getenv("TRANSCRIPT_CLI"), args...)
	// Parent SDK owns a process group; descendants remain in it for bounded Close.
	cmd.Env = []string{"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1", "CLAUDE_CODE_SAFE_MODE=1"}
	for _, k := range []string{"HOME", "PATH", "TMPDIR", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	capture, err := os.OpenFile(filepath.Join(dir, "raw.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer capture.Close()
	diagnostics, err := os.OpenFile(filepath.Join(dir, "stderr.private"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer diagnostics.Close()
	var mu sync.Mutex
	ordinal, total := 0, 0
	transfer := func(src io.Reader, dst io.Writer, direction string) error {
		scanner := bufio.NewScanner(src)
		scanner.Buffer(make([]byte, 4096), 2<<20)
		for scanner.Scan() {
			b := append([]byte(nil), scanner.Bytes()...)
			mu.Lock()
			total += len(b) + 1
			if total > 2<<20 {
				mu.Unlock()
				cancel()
				return fmt.Errorf("protocol byte limit")
			}
			ordinal++
			channel := "protocol"
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				mu.Unlock()
				cancel()
				return fmt.Errorf("non JSON frame")
			}
			if m["subtype"] == "hook_started" {
				mu.Unlock()
				cancel()
				return fmt.Errorf("startup hook violates isolation")
			}
			if t, _ := m["type"].(string); t == "control_request" || t == "control_response" || t == "control_cancel_request" {
				channel = "control"
			}
			e := json.NewEncoder(capture).Encode(transcript.Frame{Ordinal: ordinal, Direction: direction, Channel: channel, Data: b})
			mu.Unlock()
			if e != nil {
				cancel()
				return e
			}
			if _, e = dst.Write(append(b, '\n')); e != nil {
				return e
			}
		}
		if e := scanner.Err(); e != nil {
			cancel()
			return e
		}
		return nil
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() {
		if e := transfer(os.Stdin, stdin, "client"); e != nil {
			cancel()
		}
		stdin.Close()
	}()
	errCh := make(chan error, 2)
	go func() { errCh <- transfer(stdout, os.Stdout, "server") }()
	go func() {
		n, e := io.Copy(diagnostics, io.LimitReader(stderr, (256<<10)+1))
		if n > 256<<10 {
			cancel()
			e = fmt.Errorf("stderr byte limit")
		}
		errCh <- e
	}()
	first, second := <-errCh, <-errCh
	waitErr := cmd.Wait()
	if first != nil {
		return first
	}
	if second != nil {
		return second
	}
	return waitErr
}

func exactOptions(v any) bool {
	options, ok := v.([]any)
	if !ok || len(options) != 2 {
		return false
	}
	labels := map[string]bool{}
	for _, v := range options {
		o, ok := v.(map[string]any)
		if !ok {
			return false
		}
		label, _ := o["label"].(string)
		labels[label] = true
	}
	return len(labels) == 2 && labels["Blue"] && labels["Green"]
}
