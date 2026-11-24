// Package agentpi adapts Pi 1.6 RPC sessions to the unified agent contract.
//
// Each session owns a local Pi process with extension discovery disabled.
// Open and Resume use Pi's normal process permissions (fullAccess). The host
// controls any external isolation.
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
	"path/filepath"
	"slices"
	"sync"

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
		leases:    corelease.New[struct{}, *session](capabilities(), nil, nil),
		owners:    make(map[string]bool),
	}
}

func capabilities() agent.Capabilities {
	return agent.Capabilities{agent.CapabilityTextInput: true, agent.CapabilityImageInput: true}
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

func (p *Core) Open(ctx context.Context, options agent.OpenOptions) (agent.Session, error) {
	return p.attach(ctx, options, "", telemetry.Open)
}

func (p *Core) Resume(ctx context.Context, options agent.ResumeOptions) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return p.attach(ctx, options.OpenOptions, options.SessionID, telemetry.Resume)
}

func (p *Core) attach(ctx context.Context, options agent.OpenOptions, resumeID string, op telemetry.Operation) (_ agent.Session, err error) {
	ctx, observation := p.telemetry.Begin(ctx, op)
	defer func() { observation.End("", err) }()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOptions(options); err != nil {
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
		native, err := pi.Open(ctx, pi.Options{
			Command: p.config.CLIPath, Args: p.config.Args, Env: p.config.Env, Cwd: options.Cwd,
			Provider: provider, SessionDir: p.config.SessionDir, ResumeID: resumeID,
			Model: options.Config.Model, ThinkingLevel: options.Config.Effort,
			NoExtensions: true, NoTools: options.Features.RestrictLocalTools, AdditionalSystemPrompt: options.AdditionalSystemPrompt,
		})
		if err != nil {
			p.release(resumeID)
			return nil, operationError(string(op), err)
		}
		if options.Config.Effort != "" {
			if err := checkThinkingLevel(ctx, native, options.Config.Effort); err != nil {
				_ = native.CloseAndDiscard()
				p.release(resumeID)
				return nil, operationError(string(op), err)
			}
		}
		if resumeID == "" && !p.claim(native.ID()) {
			_ = native.Close()
			return nil, alreadyAttached()
		}
		return newSession(p, native, options.Features.RestrictLocalTools), nil
	})
	if err != nil {
		return nil, err
	}
	// A process that exited during the handshake can finish observation immediately.
	// Register its lease before starting observation and its release hook.
	go attached.observe()
	return attached, nil
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
	case len(o.ProcessEnv) > 0:
		return unsupported(agent.ErrorReasonProcessEnvUnsupported, "Pi per-session process environment is unsupported")
	case len(o.ShellEnv) > 0:
		return unsupported(agent.ErrorReasonShellEnvUnsupported, "Pi per-session shell environment is unsupported")
	case len(o.MCPServers) > 0 || o.MCPFiles != nil:
		return unsupported("mcp_servers_unsupported", "Pi per-session MCP configuration is unsupported")
	case o.Features.AgentQuestions != nil && *o.Features.AgentQuestions:
		return unsupported("agent_questions_unsupported", "Pi extension questions are unsupported")
	}
	return validateThinkingLevel(o.Config.Effort)
}

func unsupported(reason, msg string) error {
	return &agent.Error{Code: agent.ErrorUnsupported, Reason: reason, Message: msg}
}
