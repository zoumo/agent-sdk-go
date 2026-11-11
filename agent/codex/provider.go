package agentcodex

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/lifecycle"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

var _ agent.Provider = (*Provider)(nil)

type openingMode string

const (
	openingStart  openingMode = "open"
	openingResume openingMode = "resume"
)

// Provider owns one Codex app-server connection shared by all of its sessions.
type Provider struct {
	config         codex.StdioOptions
	tracer         agent.Tracer
	streams        streamFactory
	formsSupported bool
	lifecycle      *lifecycle.Lifecycle[*codex.Client, *session]
}

type streamStart struct {
	Transport codex.StreamTransport
	Started   func(*codex.Client)
}

type streamFactory func(context.Context) (streamStart, error)

// New constructs a Codex provider. No process or connection is created until
// Start succeeds.
func New(config Config) *Provider {
	stdio := cloneStdioOptions(config.Stdio)
	stdio.Initialize = withDefaultCapabilities(stdio.Initialize)
	provider := &Provider{
		config:         stdio,
		tracer:         config.Tracer,
		formsSupported: supportsAgentQuestions(stdio.Initialize),
	}
	provider.lifecycle = lifecycle.New[*codex.Client, *session](
		providerCapabilities(provider.formsSupported),
		provider.acquireClient,
		provider.releaseClient,
	)
	return provider
}

// Start acquires the single native connection owned by this provider.
func (p *Provider) Start(ctx context.Context) (agent.Capabilities, error) {
	return p.lifecycle.Start(ctx)
}

func (p *Provider) acquireClient(ctx context.Context) (*codex.Client, error) {
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
			ErrorReasonProviderStartFailed,
			"start Codex app-server",
			err,
		)
	}
	return client, nil
}

// Stop releases the provider-owned connection. It is terminal and succeeds
// idempotently after the first successful stop.
func (p *Provider) Stop(ctx context.Context) error {
	return p.lifecycle.Stop(ctx)
}

func (p *Provider) releaseClient(_ context.Context, client *codex.Client) error {
	if err := client.Close(); err != nil {
		return nativeOperationError("stop_provider", err)
	}
	return nil
}

// Open creates a native main session after the provider has started.
func (p *Provider) Open(
	ctx context.Context,
	options agent.OpenOptions,
) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOpenOptions(options); err != nil {
		return nil, err
	}
	mcpServers := nativeMCPServers(options.MCPServers)
	features, config, err := p.openingConfiguration(options)
	if err != nil {
		return nil, err
	}
	var created *session
	err = p.lifecycle.Open(ctx, func(
		ctx context.Context,
		client *codex.Client,
	) (*session, error) {
		native, err := codex.Open(ctx, codex.SessionOptions{
			Client:     client,
			MCPServers: mcpServers,
			Thread: codextypes.ThreadStartParams{
				Config:                config,
				Cwd:                   codextypes.Some(options.Cwd),
				DeveloperInstructions: developerInstructions(options.AdditionalSystemPrompt),
				Model:                 optionalString(options.Config.Model),
			},
		})
		if err != nil {
			return nil, nativeOperationError(string(openingStart), err)
		}
		created = newSession(p, native, features)
		return created, nil
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

// Resume restores an existing native main session. Saved turns in the native
// response are not converted into live events.
func (p *Provider) Resume(
	ctx context.Context,
	options agent.ResumeOptions,
) (agent.Session, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if err := validateOpenOptions(options.OpenOptions); err != nil {
		return nil, err
	}
	mcpServers := nativeMCPServers(options.MCPServers)
	features, config, err := p.openingConfiguration(options.OpenOptions)
	if err != nil {
		return nil, err
	}
	var created *session
	err = p.lifecycle.Open(ctx, func(
		ctx context.Context,
		client *codex.Client,
	) (*session, error) {
		native, err := codex.Open(ctx, codex.SessionOptions{
			Client:     client,
			MCPServers: mcpServers,
			Resume: &codextypes.ThreadResumeParams{
				Config: config,
				Cwd:    codextypes.Some(options.Cwd),
				DeveloperInstructions: developerInstructions(
					options.AdditionalSystemPrompt,
				),
				ExcludeTurns: codextypes.Some(true),
				Model:        optionalString(options.Config.Model),
				ThreadID:     options.SessionID,
			},
		})
		if err != nil {
			return nil, nativeOperationError(string(openingResume), err)
		}
		created = newSession(p, native, features)
		return created, nil
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

// NativeClient returns the provider-owned native connection while the provider
// is active. Callers must not close it or invoke methods that create, attach,
// resume, control, or answer sessions; only read-only native queries are allowed.
// The concrete return type preserves the design's native escape hatch; callers
// that need a compile-time restriction must bind it to a query-only interface.
func (p *Provider) NativeClient() (*codex.Client, error) {
	return p.lifecycle.Resource()
}

func providerCapabilities(formsSupported bool) agent.Capabilities {
	capabilities := agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
	}
	if formsSupported {
		capabilities[agent.CapabilityForm] = true
	}
	return capabilities
}

func sessionCapabilities(
	base agent.Capabilities,
	features agent.EffectiveFeatures,
) agent.Capabilities {
	capabilities := make(agent.Capabilities, len(base))
	maps.Copy(capabilities, base)
	if !features.AgentQuestions {
		delete(capabilities, agent.CapabilityForm)
	}
	return capabilities
}

func effectiveFeatures(
	features agent.Features,
	formsSupported bool,
) agent.EffectiveFeatures {
	return agent.EffectiveFeatures{
		AgentQuestions: features.AgentQuestionsEnabled() && formsSupported,
	}
}

func (p *Provider) openingConfiguration(
	options agent.OpenOptions,
) (
	agent.EffectiveFeatures,
	codextypes.Optional[map[string]json.RawMessage],
	error,
) {
	features := effectiveFeatures(options.Features, p.formsSupported)
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
	config := make(map[string]json.RawMessage, 2)
	if !features.AgentQuestions {
		config["tools.experimental_request_user_input.enabled"] = json.RawMessage("false")
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

func (p *Provider) sessionClosed(session *session) {
	p.lifecycle.SessionClosed(session)
}
