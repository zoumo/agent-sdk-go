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
	RunID             string
	SessionID         string
	Policy            string
	Provider          string
	Status            string
	Error             string
	Output            string
	Source            coverageSource
	Material          coverageMaterial
	MaterialStatus    string
	MaterialError     string
	ProtocolError     string
	BridgeError       string
	CleanupError      string
	ChildDetailPolicy string
	Business          json.RawMessage

	materialReady bool
}

const (
	materialStatusNotEvaluated = "not-evaluated"
	materialStatusUnavailable  = "unavailable"
	materialStatusAvailable    = "available"
	materialStatusInvalid      = "invalid"
)

type summary struct {
	SchemaVersion                int
	Status, Error, MaterialError string
	Tool                         toolIdentity
	SDK                          any
	Runs                         []runResult
}

func simulate(ctx context.Context, tool toolIdentity, o options, source sourceSelection) (err error) {
	result := summary{SchemaVersion: 1, Status: "failed", Tool: tool, SDK: sdkIdentity(o.Driver), Runs: []runResult{}}
	defer func() {
		if err == nil {
			result.Status = "passed"
		} else {
			result.Error = err.Error()
		}
		err = errors.Join(err, writeJSON(filepath.Join(o.Output, "summary.json"), result))
	}()

	first, firstErr := runHost(ctx, tool, o, source, "preserve")
	result.Runs = append(result.Runs, first)
	if first.MaterialError != "" {
		result.MaterialError = first.MaterialError
	}
	err = errors.Join(err, firstErr)
	if !first.materialReady {
		return err
	}
	if first.ChildDetailPolicy == "" {
		return err
	}

	second, secondErr := runHost(ctx, tool, o, source, "suppress")
	result.Runs = append(result.Runs, second)
	if second.MaterialError != "" && result.MaterialError == "" {
		result.MaterialError = second.MaterialError
	}
	err = errors.Join(err, secondErr)
	if firstErr == nil && secondErr == nil {
		firstDeliveries := filepath.Join(o.Output, "preserve", "sdk", "deliveries.jsonl")
		secondDeliveries := filepath.Join(o.Output, "suppress", "sdk", "deliveries.jsonl")
		if first.Provider == "codex" {
			err = errors.Join(err, codexsim.CompareDelivery(firstDeliveries, secondDeliveries))
		} else {
			err = errors.Join(err, claudesim.CompareDelivery(firstDeliveries, secondDeliveries))
		}
	}
	return err
}

func runHost(parent context.Context, tool toolIdentity, o options, source sourceSelection, policy string) (r runResult, err error) {
	r = runResult{Policy: policy, Status: "failed", Output: filepath.Join(o.Output, policy)}
	defer func() {
		if err == nil {
			r.Status = "passed"
		} else {
			r.Error = err.Error()
		}
	}()
	if err := os.Mkdir(r.Output, 0o700); err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	sessionDir := filepath.Join(r.Output, "session")
	session, err := prepareSession(ctx, tool, source, sessionDir, policy)
	if err != nil {
		r.MaterialStatus = classifyPrepareError(err)
		if r.MaterialStatus == materialStatusInvalid {
			r.MaterialError = err.Error()
		}
		return r, err
	}
	r.SessionID = session.SessionID
	r.Source = coverageSource{
		Kind:          session.Source.Kind,
		ID:            session.Source.ID,
		SHA256:        session.Source.SHA256,
		Provenance:    session.Source.Provenance,
		DriverProfile: session.Source.DriverProfile,
	}
	var pkg *materialPackage
	// Register immediately after prepare. Evidence is read before the
	// caller-owned session is removed, including after cancellation or crashes.
	defer func() {
		finalized := finalizePreparedSession(parent, session, pkg, r.RunID, r.Output, businessPassed(r.Business))
		if finalized.Evidence != nil {
			if finalized.Evidence.SessionID != session.SessionID {
				finalized.Error = errors.Join(finalized.Error, errors.New("evidence session changed"))
			}
			if len(finalized.Evidence.Executions) > 0 {
				r.RunID = finalized.Evidence.Executions[0].ID
			}
		}
		if finalized.Error != nil {
			r.CleanupError = finalized.Error.Error()
			err = errors.Join(err, fmt.Errorf("session evidence %s: %w", policy, finalized.Error))
		}
		if finalized.ProtocolError != nil {
			r.ProtocolError = finalized.ProtocolError.Error()
			err = errors.Join(err, fmt.Errorf("protocol evidence: %w", finalized.ProtocolError))
		}
	}()

	exportDir := filepath.Join(r.Output, "material")
	if err := exportPreparedSession(ctx, session, exportDir); err != nil {
		if isMaterialUnavailable(err) {
			r.MaterialStatus = materialStatusUnavailable
		} else {
			r.MaterialStatus = materialStatusInvalid
			r.MaterialError = err.Error()
		}
		return r, err
	}
	pkg, err = validateExport(exportDir, session)
	if err != nil {
		r.MaterialStatus = materialStatusInvalid
		r.MaterialError = err.Error()
		return r, err
	}
	r.MaterialStatus = materialStatusAvailable
	r.Material = coverageMaterial{
		ID:              pkg.Material.ID,
		Kind:            pkg.Material.Kind,
		Action:          pkg.Material.Action,
		Provider:        pkg.Plan.Provider,
		CaseID:          pkg.Plan.CaseID,
		Provenance:      pkg.Material.Provenance,
		PlanSHA256:      pkg.Material.PlanSHA256,
		ManifestSHA256:  pkg.Material.ManifestSHA256,
		RecordingSHA256: pkg.Material.RecordingSHA256,
		Connections:     append([]string(nil), pkg.Material.Connections...),
	}
	if session.AgentType == "claude" && len(pkg.Material.Connections) > 1 {
		return r, errors.New("claude simulation supports only the main mock connection")
	}
	r.ChildDetailPolicy = planPolicy(pkg)
	r.Provider = pkg.Plan.Provider
	r.materialReady = true
	hostRoot := filepath.Join(r.Output, "sdk")
	if err := projectAssertions(hostRoot, pkg, session, policy); err != nil {
		return r, err
	}

	peer := struct {
		FixtureDir string          `json:"fixtureDir,omitempty"`
		Connection string          `json:"connection,omitempty"`
		Remote     *remoteEndpoint `json:"remote,omitempty"`
	}{}

	var remote *mockRemoteService
	if session.Source.DriverProfile == "remote-close" {
		remote, err = startMockRemote(ctx, session.Runner, hostRoot)
		if err != nil {
			r.BridgeError = err.Error()
			return r, err
		}
		r.RunID = remote.executionID
		defer func() {
			if remote == nil {
				return
			}
			if bridgeErr := remote.close(); bridgeErr != nil {
				r.BridgeError = bridgeErr.Error()
				err = errors.Join(err, fmt.Errorf("remote bridge: %w", bridgeErr))
			}
		}()
		peer.Remote = &remote.endpoint
	} else {
		peer.FixtureDir = session.Dir
		peer.Connection = "main"
	}

	request := filepath.Join(hostRoot, "request.json")
	req := map[string]any{
		"case":      requestCase(session, pkg),
		"materials": hostRoot,
		"workspace": session.Mock.Workspace,
		"output":    hostRoot,
		"peer":      peer,
		"policy":    policy,
		"env":       session.HostEnv(),
	}
	if err := writeJSON(request, req); err != nil {
		return r, err
	}
	cmd := exec.CommandContext(ctx, o.Driver, "--request", request)
	cmd.Dir = session.Mock.Workspace
	cmd.Env = session.HostEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	stderr, openErr := os.OpenFile(filepath.Join(hostRoot, "stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if openErr != nil {
		return r, openErr
	}
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	runErr := cmd.Run()
	runErr = errors.Join(runErr, stderr.Close())
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	if remote != nil {
		if bridgeErr := remote.close(); bridgeErr != nil {
			r.BridgeError = bridgeErr.Error()
			err = errors.Join(err, fmt.Errorf("remote bridge: %w", bridgeErr))
		}
		remote = nil
	}
	err = errors.Join(err, runErr)
	businessBytes, readErr := os.ReadFile(filepath.Join(hostRoot, "result.json"))
	if readErr != nil {
		err = errors.Join(err, readErr)
	} else {
		r.Business = businessBytes
		var business struct {
			Status      string
			Message     string
			ExecutionID string `json:"executionId"`
		}
		if decodeErr := json.Unmarshal(businessBytes, &business); decodeErr != nil {
			err = errors.Join(err, decodeErr)
		} else if business.Status != "passed" {
			err = errors.Join(err, fmt.Errorf("SDK business %s: %s", business.Status, business.Message))
		}
		if business.ExecutionID != "" {
			if r.RunID != "" && r.RunID != business.ExecutionID {
				err = errors.Join(err, errors.New("SDK business execution changed"))
			}
			r.RunID = business.ExecutionID
		}
	}
	return r, errors.Join(err, ctx.Err())
}

func classifyPrepareError(err error) string {
	if isMaterialUnavailable(err) {
		return materialStatusUnavailable
	}
	return materialStatusInvalid
}

func isMaterialUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"session has no current recording",
		"fixture has no current recording",
		"absent from shared materials",
		"builtin unavailable ",
		"material unavailable",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func businessPassed(raw json.RawMessage) bool {
	var business struct{ Status string }
	return len(raw) > 0 && json.Unmarshal(raw, &business) == nil && business.Status == "passed"
}

func hostEnvironment(overrides map[string]string) []string {
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := overrides[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
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
