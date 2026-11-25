package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	pisim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/pi/go"
)

type piExtensionReplayRequest struct {
	Case, Material, Testify, Output string
}

func runPiExtensionReplay(args []string) error {
	if len(args) != 1 {
		return errors.New("use --pi-extension-replay /absolute/request.json")
	}
	var request piExtensionReplayRequest
	if err := readPiExtensionJSON(args[0], &request); err != nil {
		return err
	}
	if !filepath.IsAbs(request.Material) || !filepath.IsAbs(request.Testify) || !filepath.IsAbs(request.Output) {
		return errors.New("pi: extension replay requires absolute material, Testify executable, and new output paths")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return request.replay(ctx)
}

func (r piExtensionReplayRequest) replay(ctx context.Context) (err error) {
	material, err := readPiReplayMaterial(r.Material, r.Case)
	if err != nil {
		return err
	}
	aliasDir := filepath.Dir(material.alias)
	if err := os.Mkdir(aliasDir, 0o700); err != nil {
		return fmt.Errorf("reserve recorded extension alias directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(aliasDir)) }()

	if err := os.Mkdir(r.Output, 0o700); err != nil {
		return fmt.Errorf("create Pi replay output: %w", err)
	}
	runtimeDir, err := os.MkdirTemp("", "sdk-pi-replay-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(runtimeDir)) }()
	home := filepath.Join(runtimeDir, "home")
	workspace := filepath.Join(runtimeDir, "workspace")
	for _, dir := range []string{home, workspace} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + runtimeDir, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "pi")}
	fixture := filepath.Join(runtimeDir, "fixture")
	prepare := exec.CommandContext(ctx, r.Testify, "prepare", "--agent", "pi", "--recording", r.Material, "--fixture-dir", fixture)
	prepare.Env = env
	if diagnostics, err := prepare.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare strict Pi fixture: %w: %s", err, diagnostics)
	}
	driver, err := os.Executable()
	if err != nil {
		return err
	}
	observations, err := os.OpenFile(filepath.Join(r.Output, "observations.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, observations.Close()) }()
	options := pisim.ExtensionFlow{
		Case: r.Case, Workspace: workspace, Command: driver, Env: env,
		Args: []string{"--pi-extension-mock", r.Testify, fixture, material.alias, r.Output},
	}
	flowErr := pisim.RunExtensionFlow(ctx, options, observations, waitForPiReplayProgress(filepath.Join(r.Output, "progress")))
	return errors.Join(flowErr, verifyPiReplayResult(fixture, r.Output, material))
}

// Wait for the public full-consumption signal before the Session sends EOF.
// Testify's final outcome independently confirms the matching and cleanup.
func waitForPiReplayProgress(path string) func(context.Context, <-chan struct{}) error {
	return func(ctx context.Context, done <-chan struct{}) error {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			progress, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if string(progress) == "1" {
				return nil
			}
			if len(progress) != 0 {
				return errors.New("invalid Testify full-consumption notification")
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("wait for Testify full consumption: %w", ctx.Err())
			case <-done:
				return errors.New("pi: session ended before Testify confirmed full consumption")
			case <-ticker.C:
			}
		}
	}
}

// The SDK launches this peer as its Pi process. It forwards stdin and stdout
// directly and confines aliasing to the filesystem used by the handshake.
func runPiExtensionMock(args []string) (err error) {
	if len(args) < 6 || !validPiExtensionAlias(args[2]) {
		return errors.New("invalid Pi extension mock launch")
	}
	testify, fixture, alias, output, native := args[0], args[1], args[2], args[3], args[4:]
	index := slices.Index(native, "-e")
	if index < 0 || index+1 >= len(native) || !filepath.IsAbs(native[index+1]) || filepath.Base(native[index+1]) != "session.ts" {
		return errors.New("SDK did not launch the Pi session extension")
	}
	if err := os.Symlink(native[index+1], alias); err != nil {
		return fmt.Errorf("bind recorded extension alias: %w", err)
	}
	defer func() { err = errors.Join(err, os.Remove(alias)) }()
	configAlias := filepath.Join(filepath.Dir(alias), "session.json")
	if err := os.Symlink(filepath.Join(filepath.Dir(native[index+1]), "session.json"), configAlias); err != nil {
		return fmt.Errorf("bind recorded extension configuration alias: %w", err)
	}
	defer func() { err = errors.Join(err, os.Remove(configAlias)) }()
	paths := []string{"result.jsonl", "progress", "mock.stderr.log"}
	files := make([]*os.File, 0, len(paths))
	defer func() {
		for _, file := range files {
			err = errors.Join(err, file.Close())
		}
	}()
	for _, name := range paths {
		file, err := os.OpenFile(filepath.Join(output, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := exec.CommandContext(ctx, testify, "agent", "mock", "--fixture-dir", fixture,
		"--result-fd", "3", "--progress-fd", "4", "--timeout", "15", "--", "--mode", "rpc")
	command.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		for _, name := range []string{"PATH=", "HOME=", "TMPDIR=", "PI_CODING_AGENT_DIR="} {
			if strings.HasPrefix(entry, name) {
				return false
			}
		}
		return true
	})
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, files[2]
	command.ExtraFiles = files[:2]
	return command.Run()
}
