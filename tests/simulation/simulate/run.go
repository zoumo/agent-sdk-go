package main

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	claudesim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/claude/go"
	codexsim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/codex/go"
)

type runResult struct {
	RunID, Policy, Status, Error, Output, MaterialError string
	Business                                            json.RawMessage
}
type summary struct {
	SchemaVersion                int
	Status, Error, MaterialError string
	Tool                         toolIdentity
	SDK                          any
	Runs                         []runResult
}

func simulate(ctx context.Context, tool toolIdentity, o options, selection []string) (err error) {
	result := summary{SchemaVersion: 1, Status: "failed", Tool: tool, SDK: sdkIdentity(o.Driver), Runs: []runResult{}}
	defer func() {
		if err == nil {
			result.Status = "passed"
		} else {
			result.Error = err.Error()
		}
		err = errors.Join(err, writeJSON(filepath.Join(o.Output, "summary.json"), result))
	}()
	var s scene
	if err = toolJSON(ctx, tool.Command, append([]string{"--describe", "--json"}, selection...), &s); err != nil {
		result.MaterialError = err.Error()
		return err
	}
	if err := writeJSON(filepath.Join(o.Output, "describe.json"), s); err != nil {
		return err
	}
	if err = validateScene(s); err != nil {
		result.MaterialError = err.Error()
		return err
	}
	var plan struct {
		DriverProfile string
		Scenario      struct {
			Parameters struct{ ChildDetailPolicy string }
		}
	}
	if err := json.Unmarshal(s.AssertionInputs.SourcePlan.Original, &plan); err != nil {
		return err
	}
	policies := []string{"preserve"}
	if plan.Scenario.Parameters.ChildDetailPolicy != "" {
		policies = append(policies, "suppress")
	}
	for _, policy := range policies {
		r, e := runHost(ctx, tool, o, s, selection, policy)
		result.Runs = append(result.Runs, r)
		err = errors.Join(err, e)
	}
	if err == nil && len(policies) == 2 {
		first := filepath.Join(o.Output, "preserve", "sdk", "deliveries.jsonl")
		second := filepath.Join(o.Output, "suppress", "sdk", "deliveries.jsonl")
		if s.Provider == "codex" {
			err = codexsim.CompareDelivery(first, second)
		} else {
			err = claudesim.CompareDelivery(first, second)
		}
	}
	return err
}

func runHost(parent context.Context, tool toolIdentity, o options, s scene, selection []string, policy string) (r runResult, err error) {
	r = runResult{Policy: policy, Status: "failed", Output: filepath.Join(o.Output, policy)}
	defer func() {
		if err == nil {
			r.Status = "passed"
		} else {
			r.Error = err.Error()
		}
	}()
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	var d descriptor
	err = toolJSON(ctx, tool.Command, append(append([]string{"--prepare"}, selection...), "--output", r.Output), &d)
	if err != nil {
		// A canceled stdout read can race the tool publishing run.json. Recover
		// only this allocated output so a completed prepare is still finished.
		var prepared descriptor
		if readJSON(filepath.Join(r.Output, "run.json"), &prepared) == nil && prepared.RunID != "" {
			r.RunID = prepared.RunID
			err = errors.Join(err, finishRun(tool, r.Output, prepared))
		}
		return r, err
	}
	r.RunID = d.RunID
	// Registered immediately after prepare; independent cleanup context also runs
	// after cancellation, driver failures, invalid contracts and bridge failures.
	defer func() { err = errors.Join(err, finishRun(tool, r.Output, d)) }()
	if err = validateRun(d, s, tool); err != nil {
		r.MaterialError = err.Error()
		return r, err
	}
	hostRoot := filepath.Join(r.Output, "sdk")
	if err := projectAssertions(hostRoot, s, d); err != nil {
		return r, err
	}
	peer := struct {
		command
		Remote *remoteEndpoint `json:"remote,omitempty"`
	}{}
	for _, c := range d.Connections {
		if c.ID == "main" {
			peer.command = c.command
			if s.Provider == "claude" {
				peer.command = command{Command: c.Launcher}
			}
		}
	}
	if peer.Command == "" {
		return r, errors.New("prepared main connection missing")
	}
	var remote *mockRemoteService
	if len(d.Connections) == 2 {
		peers := make(map[string]command, 2)
		for _, c := range d.Connections {
			if c.ID == "main" || c.ID == "probe" {
				peers[c.ID] = c.command
			}
		}
		remote, err = startMockRemote(ctx, peers, hostRoot)
		if err != nil {
			return r, err
		}
		peer.Remote = &remote.endpoint
	}
	req := map[string]any{"case": s.CaseID, "materials": hostRoot, "workspace": d.Workspace, "output": hostRoot, "peer": peer, "policy": policy}
	request := filepath.Join(hostRoot, "request.json")
	if err = writeJSON(request, req); err != nil {
		if remote != nil {
			err = errors.Join(err, remote.close())
		}
		return r, err
	}
	cmd := exec.CommandContext(ctx, o.Driver, "--request", request)
	cmd.Dir = d.Workspace
	cmd.Env = hostEnvironment(d.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	// Ask the SDK host to close sessions first. A bounded group kill handles a
	// crashed/hung host without keeping supervisors/temporary locks alive.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	stderr, e := os.OpenFile(filepath.Join(hostRoot, "stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		if remote != nil {
			e = errors.Join(e, remote.close())
		}
		return r, e
	}
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	err = cmd.Run()
	err = errors.Join(err, stderr.Close())
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if remote != nil {
		err = errors.Join(err, remote.close())
	}
	b, e := os.ReadFile(filepath.Join(hostRoot, "result.json"))
	if e != nil {
		return r, errors.Join(err, e)
	}
	r.Business = b
	var business struct{ Status, Message string }
	if e = json.Unmarshal(b, &business); e != nil {
		return r, errors.Join(err, e)
	}
	if business.Status != "passed" {
		err = errors.Join(err, fmt.Errorf("SDK business %s: %s", business.Status, business.Message))
	}
	return r, errors.Join(err, ctx.Err())
}

func hostEnvironment(overrides map[string]string) []string {
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[key]; !ok {
			env = append(env, entry)
		}
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

// These are INTERNAL SDK fixture projections, never tool recording paths. The
// originals come only from describe assertionInputs; runtime paths live in their
// own file. No recorded command/config path grants launch authority.
func projectAssertions(root string, s scene, d descriptor) error {
	if err := os.Mkdir(root, 0o700); err != nil {
		return err
	}
	a := s.AssertionInputs
	if err := os.WriteFile(filepath.Join(root, "plan.json"), a.SourcePlan.Original, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "case.json"), a.SourcePlan.Original, 0o600); err != nil {
		return err
	}
	if a.SourceReport != nil {
		if err := os.WriteFile(filepath.Join(root, "report.json"), a.SourceReport.Original, 0o600); err != nil {
			return err
		}
	}
	for _, c := range a.Connections {
		path := filepath.Join(root, "raw", "raw.jsonl")
		if c.ID == "probe" {
			path = filepath.Join(root, "remote-probe", "raw", "raw.jsonl")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, c.Original, 0o600); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(root, "assertion-inputs.json"), a); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, "runtime.json"), map[string]any{"workspace": d.Workspace, "protocolCwd": d.ProtocolCwd, "config": d.Env["CLAUDE_CONFIG_DIR"]})
}

func sdkIdentity(driver string) any {
	binary, _ := os.ReadFile(driver)
	result := map[string]any{"driver": driver, "driverSHA256": digest(binary)}
	// The runner is built from the SDK checkout. Child cwd is never used to infer
	// source identity; Go build metadata and explicit binary hashes remain evidence.
	self, _ := os.Executable()
	b, _ := os.ReadFile(self)
	result["runnerSHA256"] = digest(b)
	if info, e := buildinfo.ReadFile(driver); e == nil {
		result["build"] = info
	}

	return result
}
