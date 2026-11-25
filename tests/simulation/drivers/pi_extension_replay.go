package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	agenttestify "github.com/zoumo/agent-sdk-go/replay"
	pisim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/pi/go"
)

type piExtensionReplayRequest struct {
	Case, Material, Output string
}

const (
	piMockConnection           = "main"
	piMockConsumptionDirectory = "mock-consumed"
)

func runPiExtensionReplay(args []string) error {
	if len(args) != 1 {
		return errors.New("use --pi-extension-replay /absolute/request.json")
	}
	var request piExtensionReplayRequest
	if err := readPiExtensionJSON(args[0], &request); err != nil {
		return err
	}
	if !filepath.IsAbs(request.Material) || !filepath.IsAbs(request.Output) {
		return errors.New("pi: extension replay requires absolute material and new output paths")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return request.replay(ctx)
}

func (r piExtensionReplayRequest) replay(ctx context.Context) (err error) {
	material, err := readPiReplayMaterial(r.Material)
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
	// This driver invocation runs one replay. Isolate library preparation and
	// the SDK-owned peer from inherited credentials and Pi configuration.
	os.Clearenv()
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	fixture, err := agenttestify.Prepare(agenttestify.PrepareOptions{
		FixtureDir: filepath.Join(runtimeDir, "fixture"), Agent: "pi", Recording: r.Material,
	})
	if err != nil {
		return fmt.Errorf("prepare strict Pi fixture with the Testify library: %w", err)
	}
	if fixture.AgentType != "pi" || fixture.Tool.Source != agenttestify.ToolSourceEmbedded ||
		fixture.Source.Kind != "recording" || fixture.Source.ID != "pi-controlled-"+r.Case ||
		fixture.Source.Provenance != "consumer" || fixture.Source.DriverProfile != "consumer" ||
		fixture.Current == nil || !samePiRecording(*fixture.Current, material) ||
		!slices.Equal(fixture.Current.Connections, []string{piMockConnection}) {
		return errors.New("testify: prepared Pi fixture differs from the supplied recording or requested case")
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
		Args: []string{"--pi-extension-mock", fixture.FixtureDir, material.alias, runtimeDir, r.Output},
	}
	flowErr := pisim.RunExtensionFlow(ctx, options, observations, waitForPiMockConsumption(runtimeDir))
	return errors.Join(flowErr, verifyPiReplayResult(fixture, runtimeDir, r.Output, r.Case, material))
}

// Wait for the SDK-owned peer to observe Testify's full-consumption channel
// before the Session sends EOF.
func waitForPiMockConsumption(runtimeDir string) func(context.Context, <-chan struct{}) error {
	path := filepath.Join(runtimeDir, piMockConsumptionDirectory)
	return func(ctx context.Context, done <-chan struct{}) error {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(path); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
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

// The SDK launches this peer as its Pi process. The Testify library owns the
// peer ends of those streams. Aliasing only affects the handshake filesystem.
func runPiExtensionMock(args []string) (err error) {
	if len(args) < 6 || !validPiExtensionAlias(args[1]) {
		return errors.New("invalid Pi extension mock launch")
	}
	fixtureDir, alias, runtimeDir, output, native := args[0], args[1], args[2], args[3], args[4:]
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
	diagnostics, err := os.OpenFile(filepath.Join(output, "mock.stderr.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, writeErr := fmt.Fprintln(diagnostics, err)
			err = errors.Join(err, writeErr)
		}
		err = errors.Join(err, diagnostics.Close())
	}()
	fixture, err := agenttestify.Open(fixtureDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run, startErr := fixture.StartMockAgent(ctx, agenttestify.MockAgentOptions{
		Connection: piMockConnection, Timeout: 15 * time.Second, NativeArgs: []string{"--mode", "rpc"},
	}, agenttestify.StartMockAgentStreams{
		Reader: os.Stdin, Writer: os.Stdout, Stderr: diagnostics,
	})
	if run == nil {
		return errors.Join(errors.New("testify: Pi mock failed to start"), startErr)
	}
	err = errors.Join(startErr, writePiMockStart(runtimeDir, run.Start))
	if err == nil {
		select {
		case <-run.FullConsumption():
			err = os.Mkdir(filepath.Join(runtimeDir, piMockConsumptionDirectory), 0o700)
		case <-run.Done():
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil {
		err = errors.Join(err, run.Abort())
	}
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	_, waitErr := run.Wait(settle)
	return errors.Join(err, waitErr)
}
