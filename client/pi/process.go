package pi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Options configure a native Pi process. Args precede the RPC arguments (for
// launchers such as node). ExtraArgs are resource controls; options that can
// start work or select a session or another protocol are excluded.
type Options struct {
	Command                string
	Args                   []string
	Cwd                    string
	Env                    []string // KEY=value entries overlay the process environment
	SessionDir             string
	SessionFile            string // an existing file, used with ResumeID
	ResumeID               string
	Provider               string
	Model                  string
	ThinkingLevel          string
	AdditionalSystemPrompt string // literal text
	NoTools                bool
	NoExtensions           bool
	ExtensionPaths         []string // explicit -e paths or builtin:<name>
	ExtraArgs              []string
	Stderr                 io.Writer // diagnostics; nil discards output
}

const shutdownGrace = 2 * time.Second

type process struct {
	cmd        *exec.Cmd
	promptFile string
}

func (p *process) kill() { _ = p.cmd.Process.Kill() }
func (p *process) wait() error {
	err := p.cmd.Wait()
	if p.promptFile != "" {
		err = errors.Join(err, os.Remove(p.promptFile))
	}
	return err
}

// Open starts one local Pi RPC process. Its lifetime belongs to Session;
// cancellation of ctx after the initial handshake does not stop it.
func Open(ctx context.Context, options Options) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("pi: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cwd, err := filepath.Abs(options.Cwd)
	if err != nil {
		return nil, err
	}
	options.Cwd = cwd
	args, err := options.arguments()
	if err != nil {
		return nil, err
	}
	var promptFile string
	if options.AdditionalSystemPrompt != "" {
		f, err := os.CreateTemp("", "pi-system-prompt-*")
		if err != nil {
			return nil, err
		}
		promptFile = f.Name()
		defer func() {
			if promptFile != "" {
				_ = os.Remove(promptFile)
			}
		}()
		_, writeErr := f.WriteString(options.AdditionalSystemPrompt)
		if err := errors.Join(writeErr, f.Close()); err != nil {
			return nil, err
		}
		args = append(args, "--append-system-prompt", f.Name())
	}
	command := options.Command
	if command == "" {
		command = "pi"
	}
	// The caller's context bounds the handshake. Session.Close owns process shutdown.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), command, args...)
	cmd.Dir = cwd
	cmd.Env = append(cmd.Environ(), options.Env...)
	cmd.Stderr = options.Stderr
	cmd.WaitDelay = shutdownGrace
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// The session owns the read end so Wait can reap Pi without cutting off
	// buffered records. Descendants may still hold the write end after Pi exits.
	output, childOutput, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	cmd.Stdout = childOutput
	if err = ctx.Err(); err == nil {
		err = cmd.Start()
	}
	_ = childOutput.Close()
	if err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	proc := &process{cmd: cmd, promptFile: promptFile}
	promptFile = "" // the process owns the prompt source through resource reloads
	return attach(ctx, input, output, options.ResumeID, proc)
}

func (o Options) arguments() ([]string, error) {
	args := append(slices.Clone(o.Args), "--mode", "rpc")
	for _, v := range o.Env {
		name, _, ok := strings.Cut(v, "=")
		if !ok || name == "" || strings.ContainsRune(v, 0) {
			return nil, errors.New("pi: invalid environment entry")
		}
	}
	for _, arg := range o.ExtraArgs {
		switch arg {
		case "--offline", "--no-mcp", "--no-skills", "--no-context-files", "--no-themes", "--no-prompt-templates", "--no-approve", "--no-builtin-tools":
			args = append(args, arg)
		default:
			return nil, fmt.Errorf("pi: unsupported extra argument %q", arg)
		}
	}
	for _, kv := range []struct{ flag, value string }{{"--session-dir", o.SessionDir}, {"--provider", o.Provider}, {"--model", o.Model}, {"--thinking", o.ThinkingLevel}} {
		if kv.value != "" {
			args = append(args, kv.flag, kv.value)
		}
	}
	if o.NoExtensions {
		args = append(args, "--no-extensions")
	}
	for _, path := range o.ExtensionPaths {
		if path == "" || strings.ContainsRune(path, 0) {
			return nil, errors.New("pi: invalid extension path")
		}
		args = append(args, "-e", path)
	}
	if o.NoTools {
		args = append(args, "--no-tools")
	}
	if o.ResumeID != "" {
		file, err := o.resumeFile()
		if err != nil {
			return nil, err
		}
		args = append(args, "--session", file)
	} else if o.SessionFile != "" {
		return nil, errors.New("pi: SessionFile requires an exact ResumeID")
	}
	return args, nil
}
