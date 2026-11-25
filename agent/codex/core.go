package agentcodex

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/corelease"
	"github.com/zoumo/agent-sdk-go/agent/internal/telemetry"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/mcp"
)

var _ agent.Core = (*Core)(nil)

type openingMode string

const (
	openingStart  openingMode = "open"
	openingResume openingMode = "resume"
)

// Core owns one Codex app-server connection shared by all of its sessions.
type Core struct {
	config             codex.StdioOptions
	tracer             agent.Tracer
	telemetry          telemetry.Telemetry
	streams            streamFactory
	questionsSupported bool
	leases             *corelease.Core[*codex.Client, *session]
}

type streamStart struct {
	Transport codex.StreamTransport
	Started   func(*codex.Client)
}

type streamFactory func(context.Context) (streamStart, error)

// New constructs a Codex Core. No process or connection is created until
// Start succeeds.
func New(config Config) *Core {
	stdio := cloneStdioOptions(config.Stdio)
	stdio.Initialize = withDefaultCapabilities(stdio.Initialize)
	core := &Core{
		config:             stdio,
		tracer:             config.Tracer,
		telemetry:          telemetry.New("codex", config.TracerProvider, config.MeterProvider),
		questionsSupported: supportsAgentQuestions(stdio.Initialize),
	}
	core.leases = corelease.New[*codex.Client, *session](
		providerCapabilities(),
		core.acquireClient,
		core.releaseClient,
	)
	return core
}

// Start acquires the single native connection owned by this Core.
func (p *Core) Start(ctx context.Context) (agent.Capabilities, error) {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Start)
	capabilities, reused, err := p.leases.Start(ctx)
	observation.EndLifecycle(reused, err)
	return capabilities, err
}

func (p *Core) acquireClient(ctx context.Context) (*codex.Client, error) {
	var client *codex.Client
	var err error
	if p.streams == nil {
		client, err = codex.NewStdio(ctx, p.config)
	} else {
		var stream streamStart
		stream, err = p.streams(ctx)
		if err == nil {
			client, err = codex.NewStreams(ctx, codex.StreamOptions{
				Args:       slices.Clone(p.config.Args),
				Env:        slices.Clone(p.config.Env),
				Dir:        p.config.Dir,
				Initialize: p.config.Initialize,
				Limits:     p.config.Limits,
			}, stream.Transport)
		}
		if err == nil && stream.Started != nil {
			stream.Started(client)
		}
	}
	if err != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			ErrorReasonCoreStartFailed,
			"start Codex app-server",
			err,
		)
	}
	return client, nil
}

// Stop releases the Core-owned connection. It is terminal and succeeds
// idempotently after the first successful stop.
func (p *Core) Stop(ctx context.Context) error {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Stop)
	reused, err := p.leases.Stop(ctx)
	observation.EndLifecycle(reused, err)
	return err
}

func (p *Core) releaseClient(_ context.Context, client *codex.Client) error {
	if err := client.Close(); err != nil {
		return nativeOperationError("stop_core", err)
	}
	return nil
}

// Open creates a native main session after the Core has started.
func (p *Core) Open(
	ctx context.Context,
	options agent.OpenOptions,
) (agent.Session, error) {
	if options.PermissionPreset == "" {
		options.PermissionPreset = agent.DefaultPermissionPreset
	}
	return p.OpenWithPermissions(ctx, options, Permissions{})
}

// OpenWithPermissions creates a native main session with the supplied
// per-thread permissions after the Core has started.
func (p *Core) OpenWithPermissions(
	ctx context.Context,
	options agent.OpenOptions,
	permissions Permissions,
) (_ agent.Session, err error) {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Open)
	defer func() { observation.End("", err) }()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOpenOptions(options); err != nil {
		return nil, err
	}
	permissions, err = permissions.resolve(options.PermissionPreset)
	if err != nil {
		return nil, err
	}
	mcpServers := mcp.CloneServers(options.MCPServers)
	features, config, err := p.openingConfiguration(options)
	if err != nil {
		return nil, err
	}
	created, err := p.leases.Open(ctx, func(
		ctx context.Context,
		client *codex.Client,
	) (*session, error) {
		native, err := codex.Open(ctx, codex.SessionOptions{
			Client:     client,
			MCPServers: mcpServers,
			MCPFiles:   options.MCPFiles,
			ShellEnv:   maps.Clone(options.ShellEnv),
			Thread: codextypes.ThreadStartParams{
				ApprovalPolicy:        permissions.ApprovalPolicy,
				ApprovalsReviewer:     permissions.ApprovalsReviewer,
				Config:                config,
				Cwd:                   codextypes.Some(options.Cwd),
				DeveloperInstructions: developerInstructions(options.AdditionalSystemPrompt),
				Model:                 optionalString(options.Config.Model),
				Permissions:           permissions.ProfileID,
				Sandbox:               permissions.Sandbox,
			},
		})
		if err != nil {
			return nil, nativeOperationError(string(openingStart), err)
		}
		if options.PermissionPreset != "" {
			response, ok := native.StartResponse()
			if !ok || !permissions.matches(response.ApprovalPolicy, response.ApprovalsReviewer, response.Sandbox) {
				return nil, abortPermissionMismatch(native)
			}
		}
		return newSession(p, native, features), nil
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

// Resume restores an existing native main session. Saved turns in the native
// response are not converted into live events.
func (p *Core) Resume(
	ctx context.Context,
	options agent.ResumeOptions,
) (agent.Session, error) {
	if options.PermissionPreset == "" {
		options.PermissionPreset = agent.DefaultPermissionPreset
	}
	return p.ResumeWithPermissions(ctx, options, Permissions{})
}

// ResumeWithPermissions restores an existing native main session with the
// supplied per-thread permission overrides.
func (p *Core) ResumeWithPermissions(
	ctx context.Context,
	options agent.ResumeOptions,
	permissions Permissions,
) (_ agent.Session, err error) {
	ctx, observation := p.telemetry.Begin(ctx, telemetry.Resume)
	defer func() { observation.End("", err) }()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOpenOptions(options.OpenOptions); err != nil {
		return nil, err
	}
	permissions, err = permissions.resolve(options.PermissionPreset)
	if err != nil {
		return nil, err
	}
	mcpServers := mcp.CloneServers(options.MCPServers)
	features, config, err := p.openingConfiguration(options.OpenOptions)
	if err != nil {
		return nil, err
	}
	created, err := p.leases.Open(ctx, func(
		ctx context.Context,
		client *codex.Client,
	) (*session, error) {
		native, err := codex.Open(ctx, codex.SessionOptions{
			Client:     client,
			MCPServers: mcpServers,
			MCPFiles:   options.MCPFiles,
			ShellEnv:   maps.Clone(options.ShellEnv),
			Resume: &codextypes.ThreadResumeParams{
				ApprovalPolicy:    permissions.ApprovalPolicy,
				ApprovalsReviewer: permissions.ApprovalsReviewer,
				Config:            config,
				Cwd:               codextypes.Some(options.Cwd),
				DeveloperInstructions: developerInstructions(
					options.AdditionalSystemPrompt,
				),
				ExcludeTurns: codextypes.Some(true),
				Model:        optionalString(options.Config.Model),
				Permissions:  permissions.ProfileID,
				Sandbox:      permissions.Sandbox,
				ThreadID:     options.SessionID,
			},
		})
		if err != nil {
			return nil, nativeOperationError(string(openingResume), err)
		}
		if options.PermissionPreset != "" {
			response, ok := native.ResumeResponse()
			if !ok || !permissions.matches(response.ApprovalPolicy, response.ApprovalsReviewer, response.Sandbox) {
				return nil, abortPermissionMismatch(native)
			}
		}
		return newSession(p, native, features), nil
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

// NativeClient returns the Core-owned native connection while the Core
// is active. Callers must not close it or invoke methods that create, attach,
// resume, control, or answer sessions; only read-only native queries are allowed.
// The concrete return type preserves the design's native escape hatch; callers
// that need a compile-time restriction must bind it to a query-only interface.
func (p *Core) NativeClient() (*codex.Client, error) {
	return p.leases.Resource()
}

func providerCapabilities() agent.Capabilities {
	return agent.Capabilities{
		agent.CapabilityShellEnv:          true,
		agent.CapabilityTextInput:         true,
		agent.CapabilityImageInput:        true,
		agent.CapabilityAudioInput:        true,
		agent.CapabilityResourceLinkInput: true,
		agent.CapabilityPermission:        true,
		agent.CapabilityPromptTrace:       true,
		agent.CapabilityForm:              true,
		agent.CapabilityURL:               true,
	}
}

func effectiveFeatures(
	features agent.Features,
	questionsSupported bool,
) agent.EffectiveFeatures {
	return agent.EffectiveFeatures{
		AgentQuestions:     features.AgentQuestionsEnabled() && questionsSupported,
		RestrictLocalTools: features.RestrictLocalTools,
	}
}

func (p *Core) openingConfiguration(
	options agent.OpenOptions,
) (
	agent.EffectiveFeatures,
	codextypes.Optional[map[string]json.RawMessage],
	error,
) {
	features := effectiveFeatures(options.Features, p.questionsSupported)
	config, err := threadConfig(features, options.Config.Effort)
	if err != nil {
		return features, codextypes.Optional[map[string]json.RawMessage]{}, adapterError(
			agent.ErrorInvalidArgument,
			ErrorReasonSessionConfigNotEncodable,
			"encode Codex session configuration",
			err,
		)
	}
	return features, config, nil
}

func threadConfig(
	features agent.EffectiveFeatures,
	effort string,
) (codextypes.Optional[map[string]json.RawMessage], error) {
	config := make(map[string]json.RawMessage, 5)
	if !features.AgentQuestions {
		config["tools.experimental_request_user_input.enabled"] = json.RawMessage("false")
	}
	if features.RestrictLocalTools {
		config["features.shell_tool"] = json.RawMessage("false")
		config["features.view_image"] = json.RawMessage("false")
		config["features.image_generation"] = json.RawMessage("false")
	}
	if effort != "" {
		encoded, err := json.Marshal(effort)
		if err != nil {
			return codextypes.Optional[map[string]json.RawMessage]{}, err
		}
		config["model_reasoning_effort"] = encoded
	}
	if len(config) == 0 {
		return codextypes.Optional[map[string]json.RawMessage]{}, nil
	}
	return codextypes.Some(config), nil
}

func developerInstructions(value string) codextypes.Optional[string] {
	if value == "" {
		return codextypes.Optional[string]{}
	}
	return codextypes.Some(value)
}

func validateOpenOptions(options agent.OpenOptions) error {
	if len(options.ProcessEnv) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonProcessEnvUnsupported,
			"Codex Session process environment is not supported",
			nil,
		)
	}
	if len(options.AdditionalDirs) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonAdditionalDirsUnsupported,
			"Codex additional directories are not supported by this adapter",
			nil,
		)
	}
	return nil
}

func (p *Core) sessionClosed(session *session) {
	p.leases.SessionClosed(session)
}
