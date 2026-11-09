package agentcodex

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

var _ agent.Provider = (*Provider)(nil)

type providerState uint8

const (
	providerNew providerState = iota
	providerStarted
	providerStopped
)

// Provider owns one Codex app-server connection shared by all of its sessions.
type Provider struct {
	mu             sync.Mutex
	config         codex.StdioOptions
	streams        streamFactory
	formsSupported bool
	state          providerState
	client         *codex.Client
	stopErr        error
	opening        int
	sessions       map[*session]struct{}
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
	return &Provider{
		config:         stdio,
		formsSupported: supportsAgentQuestions(stdio.Initialize),
	}
}

// Start acquires the single native connection owned by this provider.
func (p *Provider) Start(ctx context.Context) (agent.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerStarted:
		return providerCapabilities(p.formsSupported), nil
	case providerStopped:
		return nil, adapterError(
			agent.ErrorClosed,
			"provider_stopped",
			"provider has been stopped",
			nil,
		)
	}
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
			"provider_start_failed",
			"start Codex app-server",
			err,
		)
	}
	p.client = client
	p.state = providerStarted
	return providerCapabilities(p.formsSupported), nil
}

// Stop releases the provider-owned connection. It is terminal and succeeds
// idempotently after the first successful stop.
func (p *Provider) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerStopped:
		return p.stopErr
	case providerNew:
		p.state = providerStopped
		return nil
	}
	if p.opening != 0 || len(p.sessions) != 0 {
		return adapterError(
			agent.ErrorFailedPrecondition,
			"provider_has_open_sessions",
			"provider cannot stop while sessions are open",
			nil,
		)
	}
	p.state = providerStopped
	p.stopErr = p.client.Close()
	p.client = nil
	if p.stopErr != nil {
		p.stopErr = nativeOperationError("stop_provider", p.stopErr)
	}
	return p.stopErr
}

// Open creates a native main session after the provider has started.
func (p *Provider) Open(
	ctx context.Context,
	options agent.OpenOptions,
) (agent.Session, error) {
	if err := validateOpenOptions(options); err != nil {
		return nil, err
	}
	client, err := p.reserveOpen()
	if err != nil {
		return nil, err
	}
	features := effectiveFeatures(options.Features, p.formsSupported)
	native, err := codex.Open(ctx, codex.SessionOptions{
		Client: client,
		Thread: codextypes.ThreadStartParams{
			Config: threadConfig(features),
			Cwd:    codextypes.Some(options.Cwd),
		},
	})
	return p.completeOpen(native, err, "open", features)
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
	client, err := p.reserveOpen()
	if err != nil {
		return nil, err
	}
	features := effectiveFeatures(options.Features, p.formsSupported)
	native, err := codex.Open(ctx, codex.SessionOptions{
		Client: client,
		Resume: &codextypes.ThreadResumeParams{
			Config:       threadConfig(features),
			Cwd:          codextypes.Some(options.Cwd),
			ExcludeTurns: codextypes.Some(true),
			ThreadID:     options.SessionID,
		},
	})
	return p.completeOpen(native, err, "resume", features)
}

// NativeClient returns the provider-owned native connection while the provider
// is active. Callers must not close it or invoke methods that create, attach,
// resume, control, or answer sessions; only read-only native queries are allowed.
// The concrete return type preserves the design's native escape hatch; callers
// that need a compile-time restriction must bind it to a query-only interface.
func (p *Provider) NativeClient() (*codex.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == providerStopped {
		return nil, adapterError(
			agent.ErrorClosed,
			"provider_stopped",
			"provider has been stopped",
			nil,
		)
	}
	if p.state != providerStarted || p.client == nil {
		return nil, adapterError(
			agent.ErrorFailedPrecondition,
			"provider_not_started",
			"native client is available only while the provider is active",
			nil,
		)
	}
	return p.client, nil
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

func threadConfig(
	features agent.EffectiveFeatures,
) codextypes.Optional[map[string]json.RawMessage] {
	if features.AgentQuestions {
		return codextypes.Optional[map[string]json.RawMessage]{}
	}
	return codextypes.Some(map[string]json.RawMessage{
		"tools.experimental_request_user_input.enabled": json.RawMessage("false"),
	})
}

func validateOpenOptions(options agent.OpenOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if options.SystemPrompt != "" {
		return adapterError(
			agent.ErrorUnsupported,
			"system_prompt_unsupported",
			"Codex system prompts are not supported by this adapter",
			nil,
		)
	}
	if len(options.AdditionalDirs) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			"additional_dirs_unsupported",
			"Codex additional directories are not supported by this adapter",
			nil,
		)
	}
	return nil
}

func (p *Provider) reserveOpen() (*codex.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerStarted:
	case providerStopped:
		return nil, adapterError(
			agent.ErrorClosed,
			"provider_stopped",
			"provider has been stopped",
			nil,
		)
	default:
		return nil, adapterError(
			agent.ErrorFailedPrecondition,
			"provider_not_started",
			"provider must be started before opening a session",
			nil,
		)
	}
	p.opening++
	return p.client, nil
}

func (p *Provider) completeOpen(
	native *codex.Session,
	openErr error,
	operation string,
	features agent.EffectiveFeatures,
) (agent.Session, error) {
	p.mu.Lock()
	p.opening--
	if openErr != nil {
		p.mu.Unlock()
		return nil, nativeOperationError(operation, openErr)
	}
	created := newSession(p, native, features)
	if p.sessions == nil {
		p.sessions = make(map[*session]struct{})
	}
	p.sessions[created] = struct{}{}
	p.mu.Unlock()
	return created, nil
}

func (p *Provider) sessionClosed(session *session) {
	p.mu.Lock()
	delete(p.sessions, session)
	p.mu.Unlock()
}
