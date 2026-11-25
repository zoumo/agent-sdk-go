// Package agentpi adapts Pi 1.6 RPC sessions to the unified agent contract.
//
// Each session owns a local Pi process with automatic extension discovery disabled.
// Open and Resume use Pi's normal process permissions (fullAccess). The host
// controls any external isolation. Open and Resume accept ProcessEnv as a
// child process overlay. On macOS and Linux, ShellEnv loads a Pi 1.6.0
// extension that applies its overlay to the model-facing bash tool.
//
// Use OpenWithOptions or ResumeWithOptions to require approval before local
// write, edit, bash, and PowerShell tools. Set agent.Features.AgentQuestions
// explicitly to true to expose the single-question tool. Each dialog uses a live
// InteractionRef, expires independently and accepts one answer. Pi's response
// channel confirms a local frame write. The tool result reports its outcome.
//
// StartOrSteer accepts one optional text block followed by inline images.
// A queued receipt confirms native admission. Watch for an idle state update
// after Interrupt before submitting new work that must run independently.
// Pi sessions do not implement agent.Injector.
//
// To select a model, set Config.Provider and pass the model ID in
// agent.OpenOptions.Config. If the initial model is omitted, Pi selects its
// default. Config.Provider also supplies the provider for runtime model changes.
package agentpi

import (
	"context"
	"errors"
	"maps"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/corelease"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/pi"
)

// Config selects the Pi executable and persistent session scope.
type Config struct {
	CLIPath string
	// Args are launcher arguments, such as a CLI script when launching Node.js.
	Args []string
	// Env overlays the inherited process environment.
	Env []string
	// SessionDir selects a Pi session directory for both Open and exact Resume.
	SessionDir string
	// Provider qualifies the initial model when one is supplied and is required
	// for runtime model changes.
	Provider       string
	Tracer         agent.Tracer
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

// Core owns one lease per attached Pi process.
type Core struct {
	config    Config
	telemetry telemetry.Telemetry
	leases    *corelease.Core[struct{}, *session]
	mu        sync.Mutex
	owners    map[string]bool
}

var _ agent.Core = (*Core)(nil)

// New prepares a Core without starting Pi.
func New(config Config) *Core {
	config.Args = slices.Clone(config.Args)
	config.Env = slices.Clone(config.Env)
	return &Core{
		config:    config,
		telemetry: telemetry.New("pi", config.TracerProvider, config.MeterProvider),
		leases:    corelease.New[struct{}, *session](capabilities(shellEnvSupported(), true, true), nil, nil),
		owners:    make(map[string]bool),
	}
}

func capabilities(shellEnv, approval, questions bool) agent.Capabilities {
	return agent.Capabilities{
		agent.CapabilityTextInput: true, agent.CapabilityImageInput: true,
		agent.CapabilityProcessEnv: true, agent.CapabilityShellEnv: shellEnv,
		agent.CapabilityPermission: approval, agent.CapabilityForm: questions,
	}
}

func shellEnvSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func (p *Core) Start(ctx context.Context) (agent.Capabilities, error) {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Start)
	caps, reused, err := p.leases.Start(ctx)
	observation.EndLifecycle(reused, err)
	return caps, err
}

func (p *Core) Stop(ctx context.Context) error {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Stop)
	reused, err := p.leases.Stop(ctx)
	observation.EndLifecycle(reused, err)
	return err
}

// SessionOptions contains the Pi-specific settings for one Open or Resume.
type SessionOptions struct {
	// ToolApproval asks for confirmation before Pi's local write, edit,
	// bash, and PowerShell tools execute. It provides no sandbox.
	ToolApproval bool
	// InteractionTimeout bounds each SDK dialog. Zero selects one minute.
	InteractionTimeout time.Duration
}

func (p *Core) Open(ctx context.Context, options agent.OpenOptions) (agent.Session, error) {
	return p.OpenWithOptions(ctx, options, SessionOptions{})
}

// OpenWithOptions starts a Pi session with additional per-session settings.
func (p *Core) OpenWithOptions(ctx context.Context, options agent.OpenOptions, piOptions SessionOptions) (agent.Session, error) {
	return p.attach(ctx, options, piOptions, "", telemetry.Open)
}

func (p *Core) Resume(ctx context.Context, options agent.ResumeOptions) (agent.Session, error) {
	return p.ResumeWithOptions(ctx, options, SessionOptions{})
}

// ResumeWithOptions starts Pi for an exact persisted session with new settings.
func (p *Core) ResumeWithOptions(ctx context.Context, options agent.ResumeOptions, piOptions SessionOptions) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return p.attach(ctx, options.OpenOptions, piOptions, options.SessionID, telemetry.Resume)
}

func (p *Core) attach(ctx context.Context, options agent.OpenOptions, piOptions SessionOptions, resumeID string, op telemetry.Operation) (_ agent.Session, err error) {
	ctx, observation := p.telemetry.Begin(ctx, op)
	defer func() { observation.End("", err) }()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	extensionConfig, err := makeSessionExtensionConfig(options, piOptions)
	if err != nil {
		return nil, err
	}
	if p.config.SessionDir != "" && !filepath.IsAbs(p.config.SessionDir) {
		return nil, &agent.Error{Code: agent.ErrorInvalidArgument, Reason: "session_dir_not_absolute", Message: "Pi session directory must be absolute"}
	}
	provider := p.config.Provider
	if options.Config.Model == "" {
		provider = ""
	}
	attached, err := p.leases.Open(ctx, func(ctx context.Context, _ struct{}) (*session, error) {
		if resumeID != "" && !p.claim(resumeID) {
			return nil, alreadyAttached()
		}
		var attached bool
		defer func() {
			if !attached && resumeID != "" {
				p.release(resumeID)
			}
		}()
		launch := pi.Options{
			Command: p.config.CLIPath, Args: p.config.Args, Env: p.processEnvironment(options.ProcessEnv), Cwd: options.Cwd,
			Provider: provider, SessionDir: p.config.SessionDir, ResumeID: resumeID,
			Model: options.Config.Model, ThinkingLevel: options.Config.Effort,
			NoExtensions: true, NoTools: options.Features.RestrictLocalTools, AdditionalSystemPrompt: options.AdditionalSystemPrompt,
		}
		extension, err := materializeSessionExtension(extensionConfig)
		if err != nil {
			return nil, operationError(string(op), err)
		}
		if extension != nil {
			launch.ExtensionPaths = []string{extension.path}
		}
		native, err := pi.Open(ctx, launch)
		if err != nil {
			return nil, operationError(string(op), errors.Join(err, extension.remove()))
		}
		if err := extension.check(ctx, native); err != nil {
			return nil, errors.Join(operationError(string(op), err), native.CloseAndDiscard(), extension.remove())
		}
		if options.Config.Effort != "" {
			if err := checkThinkingLevel(ctx, native, options.Config.Effort); err != nil {
				return nil, errors.Join(operationError(string(op), err), native.CloseAndDiscard(), extension.remove())
			}
		}
		if resumeID == "" && !p.claim(native.ID()) {
			return nil, errors.Join(alreadyAttached(), native.CloseAndDiscard(), extension.remove())
		}
		attached = true
		return newSession(p, native, options.Features.RestrictLocalTools, extension), nil
	})
	if err != nil {
		return nil, err
	}
	// A process that exited during the handshake can finish observation immediately.
	// Register its lease before starting observation and its release hook.
	go attached.observe()
	return attached, nil
}

func (p *Core) processEnvironment(overlay map[string]string) []string {
	env := slices.Clone(p.config.Env)
	for _, key := range slices.Sorted(maps.Keys(overlay)) {
		env = append(env, key+"="+overlay[key])
	}
	return env
}

func (p *Core) claim(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owners[id] {
		return false
	}
	p.owners[id] = true
	return true
}

func (p *Core) release(id string) {
	p.mu.Lock()
	delete(p.owners, id)
	p.mu.Unlock()
}

func alreadyAttached() error {
	return &agent.Error{Code: agent.ErrorFailedPrecondition, Reason: "session_already_attached", Message: "Pi session already has a live attachment"}
}

func validateOptions(o agent.OpenOptions) error {
	preset := o.PermissionPreset
	if preset == "" {
		preset = agent.DefaultPermissionPreset
	}
	switch {
	case preset != agent.PermissionPresetFullAccess:
		return unsupported("permission_preset_unsupported", "Pi supports only fullAccess")
	case len(o.AdditionalDirs) > 0:
		return unsupported(agent.ErrorReasonAdditionalDirsUnsupported, "Pi additional directories are unsupported")
	case len(o.ShellEnv) > 0 && (!shellEnvSupported() || o.Features.RestrictLocalTools):
		return unsupported(agent.ErrorReasonShellEnvUnsupported, "Pi ShellEnv requires the local bash tool on macOS or Linux")
	case len(o.MCPServers) > 0 || o.MCPFiles != nil:
		return unsupported("mcp_servers_unsupported", "Pi per-session MCP configuration is unsupported")
	}
	return validateThinkingLevel(o.Config.Effort)
}

func makeSessionExtensionConfig(options agent.OpenOptions, piOptions SessionOptions) (sessionExtensionConfig, error) {
	timeout := piOptions.InteractionTimeout
	if timeout < 0 || timeout > time.Duration(math.MaxInt32)*time.Millisecond || timeout%time.Millisecond != 0 {
		return sessionExtensionConfig{}, &agent.Error{
			Code: agent.ErrorInvalidArgument, Reason: "pi_interaction_timeout_invalid",
			Message: "Pi interaction timeout must be a nonnegative whole millisecond within the native timer range",
		}
	}
	if piOptions.ToolApproval && options.Features.RestrictLocalTools {
		return sessionExtensionConfig{}, unsupported("pi_tool_approval_unavailable", "Pi tool approval requires local tools")
	}
	questions := options.Features.AgentQuestions != nil && *options.Features.AgentQuestions
	config := sessionExtensionConfig{ShellEnv: options.ShellEnv, ToolApproval: piOptions.ToolApproval, AgentQuestions: questions}
	if config.ToolApproval || config.AgentQuestions {
		if timeout == 0 {
			timeout = time.Minute
		}
		config.InteractionTimeoutMS = timeout.Milliseconds()
	}
	return config, nil
}

func unsupported(reason, msg string) error {
	return &agent.Error{Code: agent.ErrorUnsupported, Reason: reason, Message: msg}
}
