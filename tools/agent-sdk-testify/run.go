package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func run(ctx context.Context, o options) (object, error) {
	var err error
	o.Materials, err = filepath.Abs(o.Materials)
	if err != nil {
		return nil, err
	}
	plan, err := loadMaterials(o)
	if err != nil {
		return nil, err
	}
	provider := str(plan["provider"])
	if provider != "codex" && provider != "claude" {
		return nil, errors.New("materials must identify codex or claude")
	}
	if o.Output == "" {
		o.Output, err = os.MkdirTemp("", "agent-sdk-testify-")
	} else {
		o.Output, err = filepath.Abs(o.Output)
		if err == nil {
			err = os.Mkdir(o.Output, 0700)
		}
	}
	if err != nil {
		return nil, err
	}
	o.Output, err = filepath.EvalSymlinks(o.Output)
	if err != nil {
		return nil, err
	}
	root := o.Output
	workspace := filepath.Join(o.Output, "project")
	defer scrubNative(root)
	for _, dir := range []string{root, workspace, filepath.Join(root, "home"), filepath.Join(root, "config"), filepath.Join(root, "tmp"), filepath.Join(o.Output, "materials")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	if err = writeFiles(workspace, obj(obj(plan["scenario"])["files"])); err != nil {
		return nil, err
	}
	if err = copySaved(o.Materials, root); err != nil {
		return nil, err
	}
	if o.Mode == "record" && plan["driverProfile"] == "existing-children" && o.HistorySeed != "" {
		if err = prepareHistorySeed(o.HistorySeed, root, plan); err != nil {
			return nil, err
		}
	}
	if o.Mode == "record" && plan["driverProfile"] == "existing-children" && len(obj(plan["historySeed"])) == 0 {
		return nil, errors.New("existing-child recording needs supplied saved-history materials")
	}
	if o.Mode != "record" {
		if _, err = os.Stat(filepath.Join(o.Materials, "raw/raw.jsonl")); err != nil {
			return nil, fmt.Errorf("offline recording: %w", err)
		}
		// The original native cwd is protocol data. Only writable fixture files
		// and native history copies move; original recordings remain untouched.
		for _, name := range []string{"raw", "remote-probe/raw"} {
			source := filepath.Join(o.Materials, name)
			if _, e := os.Stat(source); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return nil, e
			}
			target := filepath.Join(root, name)
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return nil, err
			}
			if err = os.Symlink(source, target); err != nil {
				return nil, err
			}
		}
		for _, name := range []string{"remote-service.json"} {
			if _, e := os.Stat(filepath.Join(o.Materials, name)); e == nil {
				if err = copyFile(filepath.Join(o.Materials, name), filepath.Join(root, name)); err != nil {
					return nil, err
				}
			}
		}
	} else {
		plan["project"] = workspace
		if err = prepareNative(ctx, &o, plan, root); err != nil {
			return nil, err
		}
	}
	plan["root"] = root
	plan["home"] = filepath.Join(root, "home")
	plan["config"] = filepath.Join(root, "config")
	plan["raw"] = filepath.Join(root, "raw")
	plan["_fixtureProject"] = workspace
	if str(plan["project"]) == "" {
		plan["project"] = workspace
	}
	peerName := o.Peer
	if peerName == "" {
		var err error
		peerName, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	peer, err := absoluteExecutable(peerName)
	if err != nil {
		return nil, err
	}
	plan["_mock"] = peer
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var service *remoteService
	if o.Mode == "record" && plan["driverProfile"] == "remote-close" {
		service, err = startRemote(ctx, o, plan)
		if err != nil {
			return nil, err
		}
		defer func() {
			if service != nil {
				_ = service.close()
			}
		}()
	}
	peers := object{}
	for _, phase := range []string{"main", "probe"} {
		base := root
		source := o.Materials
		if phase == "probe" {
			base = filepath.Join(root, "remote-probe")
			source = filepath.Join(o.Materials, "remote-probe")
			if err = os.MkdirAll(base, 0700); err != nil {
				return nil, err
			}
		}
		var args []string
		if o.Mode == "record" {
			if phase == "probe" && service == nil {
				continue
			}
			cwd := str(plan["project"])
			if plan["driverProfile"] == "existing-children" {
				cwd = str(plan["historyProject"])
			}
			args = []string{"protocol", "record", "--output", filepath.Join(base, "raw"), "--cwd", cwd, "--stdin-eof-action", str(plan["stdinEOFAction"]), "--", str(plan["cli"])}
			args = append(args, stringsOf(plan["args"])...)
			if service != nil {
				args = []string{"protocol", "record", "--output", filepath.Join(base, "raw"), "--cwd", str(plan["project"]), "--", peer, "protocol", "connect", "--url", service.endpoint, "--token-file", service.token}
			}
		} else {
			args = []string{"protocol", "mock", "--provider", provider, "--scenario", filepath.Join(source, "raw/raw.jsonl"), "--receipt", filepath.Join(base, "replay-peer.json")}
		}
		peerPath := filepath.Join(o.Output, "peer-"+phase+".json")
		if err = writeJSON(peerPath, command{Command: peer, Args: args}); err != nil {
			return nil, err
		}
		peers[phase] = command{Command: self, Args: []string{"peer", "--request", peerPath}}
	}
	plan["_peers"] = peers
	// Keep original source data for provider-owned assertions; mutable runtime
	// paths belong to this attempt. No exact SDK or executable version gate.
	if err = writeJSON(filepath.Join(o.Output, "materials/case.json"), plan); err != nil {
		return nil, err
	}
	req := request{Case: str(plan["caseId"]), Materials: filepath.Join(o.Output, "materials"), Workspace: workspace, Peer: peers["main"].(command), Output: root}
	requestPath := filepath.Join(o.Output, "request.json")
	if err = writeJSON(requestPath, req); err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(root, "plan.json"), plan); err != nil {
		return nil, err
	}
	if _, e := os.Stat(filepath.Join(o.Materials, "report.json")); o.Mode != "record" && e == nil {
		if err = copyFile(filepath.Join(o.Materials, "report.json"), filepath.Join(root, "source-report.json")); err != nil {
			return nil, err
		}
	}
	if _, e := os.Stat(filepath.Join(root, "source-report.json")); e == nil {
		if err = copyFile(filepath.Join(root, "source-report.json"), filepath.Join(root, "report.json")); err != nil {
			return nil, err
		}
	}
	driver, err := absoluteExecutable(o.Driver)
	if err != nil {
		return nil, err
	}
	env, err := driverEnvironment(o, plan)
	if err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(o.Output, "stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	stdout, err := os.OpenFile(filepath.Join(o.Output, "stdout.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer stdout.Close()
	cmd := exec.Command(driver, append(append([]string{}, o.DriverArgs...), "--request", requestPath)...)
	cmd.Dir = workspace
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = log
	runErr := waitDriver(ctx, cmd)
	if service != nil {
		runErr = errors.Join(runErr, service.close())
		service = nil
	}
	var outcome result
	resultErr := readJSON(filepath.Join(root, "result.json"), &outcome)
	if resultErr == nil {
		switch outcome.Status {
		case "passed", "failed", "untriggered", "unsupported":
		default:
			resultErr = fmt.Errorf("invalid driver status %q", outcome.Status)
		}
	}
	transportErr := checkTransport(o.Mode, root, provider, outcome.Status)
	if o.Mode == "record" {
		transportErr = errors.Join(transportErr, writeSourceManifest(root, outcome))
	}
	runErr = errors.Join(runErr, resultErr, transportErr)
	summary := object{"case": req.Case, "provider": provider, "mode": o.Mode, "result": outcome, "output": o.Output}
	if runErr != nil {
		summary["error"] = runErr.Error()
	}
	// Versions and hashes aid diagnosis; they never select or reject a runtime.
	summary["driver"] = driver
	summary["driverArgs"] = o.DriverArgs
	if b, e := os.ReadFile(filepath.Join(root, "raw/raw.jsonl")); e == nil {
		h := sha256.Sum256(b)
		summary["recordingSHA256"] = hex.EncodeToString(h[:])
	}
	if err = writeJSON(filepath.Join(o.Output, "summary.json"), summary); err != nil {
		return summary, errors.Join(runErr, err)
	}
	if runErr != nil {
		return summary, runErr
	}
	if outcome.Status != "passed" {
		return summary, fmt.Errorf("driver reported %s: %s", outcome.Status, outcome.Message)
	}
	return summary, nil
}

func waitDriver(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Also reap owned descendants when a driver crashes or exits without
	// closing them. The normal SDK close path finishes before driver exit.
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		return ctx.Err()
	}
}

func checkTransport(mode, root, provider, status string) error {
	if status == "unsupported" {
		return nil
	}
	for _, phase := range []string{"", "remote-probe", "suppress"} {
		base := filepath.Join(root, phase)
		if phase != "" {
			if _, e := os.Stat(filepath.Join(base, "raw/raw.jsonl")); os.IsNotExist(e) {
				continue
			}
		}
		if mode == "record" {
			var t object
			if err := readJSON(filepath.Join(base, "raw/transport.json"), &t); err != nil {
				return err
			}
			if t["captureStatus"] != "complete" || t["cleanupComplete"] != true {
				return errors.New("recording did not complete and clean up")
			}
			continue
		}
		var receipt object
		if err := readJSON(filepath.Join(base, "replay-peer.json"), &receipt); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(base, "raw/raw.jsonl"))
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		if receipt["provider"] != provider || receipt["fullConsumption"] != true || receipt["clientEOF"] != true || receipt["nativeLaunched"] != false || receipt["recordingSHA256"] != hex.EncodeToString(h[:]) {
			return errors.New("mock receipt does not prove full consumption of the selected recording")
		}
	}
	return nil
}

func driverEnvironment(o options, plan object) ([]string, error) {
	env := map[string]string{"PATH": os.Getenv("PATH"), "HOME": str(plan["home"]), "TMPDIR": filepath.Join(str(plan["root"]), "tmp"), "LANG": "C.UTF-8"}
	if str(plan["provider"]) == "codex" {
		env["CODEX_HOME"] = str(plan["config"])
	} else {
		env["CLAUDE_CONFIG_DIR"] = str(plan["config"])
		env["CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"] = "1"
		env["DISABLE_AUTOUPDATER"] = "1"
	}
	if o.Mode == "record" {
		var selected map[string]string
		if err := readJSON(filepath.Join(str(plan["root"]), "native.env.private.json"), &selected); err != nil {
			return nil, err
		}
		for k, v := range selected {
			env[k] = v
		}
	}
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out, nil
}
