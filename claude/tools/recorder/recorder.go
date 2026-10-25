// SPDX-License-Identifier: Apache-2.0
// Explicit manual recorder. Default tests exercise guards without recording.
package recorder

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	claude "github.com/zoumo/agent-sdk-go/claude"
	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

const s1 = "Read only numbers.txt (two lines: 7 and 11), then use Write once to create summary.txt containing exactly total=18 followed by a newline. Use no other tools or files. Give one short final sentence."
const s2 = "Use AskUserQuestion exactly once with the question 'Which synthetic color?' and exactly two options Blue and Green. Wait for the answer, then give one short final sentence. Do not use other tools."

// Run executes the explicitly guarded recorder using the process arguments and environment.
func Run() (runErr error) {
	if os.Getenv("TRANSCRIPT_RECORD_PEER") == "1" {
		return proxy()
	}
	mode := flag.String("mode", "", "preflight, record or e2e")
	story := flag.String("story", "", "s1, s2, r1, r2 or r3 (root only)")
	cli := flag.String("cli", "", "absolute approved CLI")
	digest := flag.String("cli-sha256", "", "approved exact CLI SHA256")
	ledgerPath := flag.String("session-ledger", "", "one durable aggregate ledger directory across tickets")
	out := flag.String("private-dir", "", "new private capture directory outside Git")
	persist := flag.Bool("synthetic-persistence", false, "explicit isolated R1 synthetic storage for later local R4 queries")
	sdkRevision := flag.String("sdk-revision", "", "explicit source revision when build provenance is unavailable")
	flag.Parse()
	if *mode == "preflight" {
		return Preflight()
	}
	if err := transcript.LiveGuard(*mode, os.Getenv("SDK_REAL_AGENT_E2E"), os.Getenv("CI"), *cli); err != nil {
		return err
	}
	plan, err := Plan(*story)
	if err != nil {
		return err
	}
	if *persist != plan.Persist {
		return fmt.Errorf("R1 requires explicit synthetic-persistence; other scenes forbid persistence")
	}
	// No version/help executable or auth query can run before independent proof.
	if err = Preflight(); err != nil {
		return err
	}
	buildInfo, _ := debug.ReadBuildInfo()
	provenance, err := recordingRevision(buildInfo, *sdkRevision)
	if err != nil {
		return err
	}
	ledger, err := OpenLedger(*ledgerPath)
	if err != nil {
		return err
	}
	if err = ledger.Reserve(*story); err != nil {
		return err
	}
	driven := false
	defer func() {
		if !driven {
			runErr = errors.Join(runErr, ledger.Finish(*story, false))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	activity, stopActivity := activityContext(ctx)
	defer stopActivity()
	cliDigest, err := inspectCLI(activity, *cli, *digest)
	if err != nil {
		return err
	}
	workspace, err := PrepareWorkspace(*out, *story)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := append(workspace.Environment(), "SDK_REAL_AGENT_E2E=1", "TRANSCRIPT_MODE="+*mode, "TRANSCRIPT_RECORD_PEER=1", "TRANSCRIPT_CLI="+*cli, "TRANSCRIPT_PRIVATE="+*out, "TRANSCRIPT_STORY="+*story)
	observations, err := os.OpenFile(filepath.Join(*out, "observations.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer observations.Close()
	client, err := claude.Open(activity, claude.SessionOptions{CLIPath: exe, WorkDir: workspace.Project, Env: env, InitializeTimeout: 30 * time.Second, ShutdownTimeout: 3 * time.Second, Limits: claude.Limits{FrameBytes: 2 << 20, StderrBytes: 256 << 10}})
	if err != nil {
		return err
	}
	driven = true
	runErr = Drive(ctx, client, *story, workspace, ledger, observations)
	summary := map[string]any{"story": *story, "seconds": time.Since(start).Seconds(), "root_attempts": 1, "reserved_turns": plan.Turns, "children": 0, "error": runErr != nil, "prompts": plan.Prompts, "cli_version": CLIVersion, "cli_sha256": cliDigest, "sdk_revision": provenance.Revision, "sdk_revision_source": provenance.Source, "sdk_modified": provenance.Modified, "synthetic_persistence": plan.Persist}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return errors.Join(runErr, err)
	}
	return errors.Join(runErr, os.WriteFile(filepath.Join(*out, "status.json"), b, 0600))
}
func proxy() error {
	if err := transcript.LiveGuard(os.Getenv("TRANSCRIPT_MODE"), os.Getenv("SDK_REAL_AGENT_E2E"), os.Getenv("CI"), os.Getenv("TRANSCRIPT_CLI")); err != nil {
		return err
	}
	if err := Preflight(); err != nil {
		return err
	}
	if _, err := Plan(os.Getenv("TRANSCRIPT_STORY")); err != nil {
		return err
	}
	dir := os.Getenv("TRANSCRIPT_PRIVATE")
	workspace := &Workspace{Root: dir, Project: filepath.Join(dir, "project"), Config: filepath.Join(dir, "config"), Home: filepath.Join(dir, "home"), Temp: filepath.Join(dir, "tmp"), owned: dir}
	var err error
	workspace.identity, err = os.Lstat(dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	planned, err := Arguments(os.Getenv("TRANSCRIPT_STORY"), workspace)
	if err != nil {
		return err
	}
	// The SDK supplies only these exact transport flags. Never pass arbitrary
	// proxy arguments (especially --resume) through to the native CLI.
	expected := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	if !reflect.DeepEqual(os.Args[1:], expected) {
		return fmt.Errorf("unreviewed SDK startup flags")
	}
	args := append(append([]string{}, expected...), planned...)
	cmd := exec.CommandContext(ctx, os.Getenv("TRANSCRIPT_CLI"), args...)
	// Parent SDK owns a process group; descendants remain in it for bounded Close.
	cmd.Env = workspace.Environment()
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

func exactOptions(v any) bool { return exactLabels(v, "Blue", "Green") }
func exactLabels(v any, first, second string) bool {
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
	return len(labels) == 2 && labels[first] && labels[second]
}
