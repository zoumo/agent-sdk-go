package agentclaude

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

var _ agent.Provider = (*Provider)(nil)

const (
	defaultInitializeWait = 60 * time.Second
	maxInitBufferedEvents = 128
	maxInitBufferedBytes  = 4 << 20
)

type providerState uint8

const (
	providerNew providerState = iota
	providerStarted
	providerStopped
)

// Provider owns the lifecycle of the Claude sessions it creates. Each Claude
// session owns its native CLI process.
type Provider struct {
	mu       sync.Mutex
	config   Config
	streams  streamFactory
	state    providerState
	opening  int
	sessions map[*session]struct{}
	stopErr  error
}

type streamOpen struct {
	Transport claude.StreamTransport
	Workspace string
	Env       []string
	Started   func(*claude.Session)
}

type streamFactory func(context.Context, claude.SessionOptions) (streamOpen, error)

// New constructs a Claude provider without starting a native process.
func New(config Config) *Provider {
	return &Provider{config: cloneConfig(config)}
}

// Start marks this provider ready to open sessions.
func (p *Provider) Start(context.Context) (agent.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerStarted:
		return providerCapabilities(), nil
	case providerStopped:
		return nil, adapterError(
			agent.ErrorClosed,
			"provider_stopped",
			"provider has been stopped",
			nil,
		)
	default:
		p.state = providerStarted
		return providerCapabilities(), nil
	}
}

// Stop rejects shutdown while any session is opening or open. It is terminal
// and idempotent.
func (p *Provider) Stop(context.Context) error {
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
	return p.stopErr
}

// Open creates a new Claude session after native initialization confirms its
// real identity.
func (p *Provider) Open(
	ctx context.Context,
	options agent.OpenOptions,
) (agent.Session, error) {
	if err := validateOpenOptions(options); err != nil {
		return nil, err
	}
	return p.open(ctx, options, "")
}

// Resume confirms the supported CLI version and saved transcript before launch,
// then restores the requested session after live init or its private callback
// confirms the same identity.
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
	if ctx == nil {
		return nil, adapterError(
			agent.ErrorInvalidArgument,
			"nil_context",
			"resume context is required",
			nil,
		)
	}
	mcpServers, err := claudeMCPServers(options.MCPServers)
	if err != nil {
		return nil, err
	}
	if err := p.validateResumeConfig(); err != nil {
		return nil, err
	}
	if err := p.reserveOpen(); err != nil {
		return nil, err
	}
	attempt, err := p.prepareResume(ctx, options)
	if err != nil {
		p.finishOpen()
		return nil, err
	}
	return p.openReserved(
		ctx,
		options.OpenOptions,
		options.SessionID,
		attempt,
		mcpServers,
	)
}

func (p *Provider) open(
	ctx context.Context,
	options agent.OpenOptions,
	resumeID string,
) (agent.Session, error) {
	mcpServers, err := claudeMCPServers(options.MCPServers)
	if err != nil {
		return nil, err
	}
	if err := p.reserveOpen(); err != nil {
		return nil, err
	}
	return p.openReserved(ctx, options, resumeID, nil, mcpServers)
}

func (p *Provider) openReserved(
	ctx context.Context,
	options agent.OpenOptions,
	resumeID string,
	resume *resumeAttempt,
	mcpServers []claude.MCPServer,
) (agent.Session, error) {
	nativeOptions := p.nativeOptions(
		options.Cwd,
		resumeID,
		options.Features,
		mcpServers,
		options.Execution,
	)
	if resume != nil {
		nativeOptions.SessionStartProbe = &resume.probe
	}
	var stream *streamOpen
	if p.streams != nil {
		opened, err := p.streams(ctx, claude.SessionOptions(nativeOptions))
		if err != nil {
			p.finishOpen()
			return nil, nativeOperationError("open", err)
		}
		stream = &opened
	}
	if stream != nil {
		if stream.Workspace != "" {
			nativeOptions.WorkDir = stream.Workspace
		}
		if stream.Env != nil {
			nativeOptions.Env = slices.Clone(stream.Env)
		}
	}
	var native *claude.Session
	var err error
	if stream == nil {
		native, err = claude.Open(ctx, claude.SessionOptions(nativeOptions))
	} else {
		native, err = claude.OpenStreams(ctx, claude.SessionOptions(nativeOptions), stream.Transport)
	}
	if err != nil {
		p.finishOpen()
		return nil, nativeOperationError("open", err)
	}
	if stream != nil && stream.Started != nil {
		stream.Started(native)
	}
	initContext, cancelInit := waitForInitContext(ctx, p.config.InitializeTimeout)
	var (
		init     *claudetypes.Event
		buffered []claude.Received
		guard    *resumeIdentityGuard
	)
	if resume == nil {
		init, buffered, err = waitForInit(initContext, native)
	} else {
		init, buffered, guard, err = waitForResumeIdentity(
			initContext,
			native,
			resume.target,
			resume.probe.CallbackID,
		)
	}
	cancelInit()
	if err != nil {
		p.finishOpen()
		return nil, closeAfterOpenFailure(native, err)
	}
	if init != nil {
		if resumeID != "" && init.SessionID != resumeID {
			p.finishOpen()
			return nil, closeAfterOpenFailure(native, adapterError(
				agent.ErrorNativeRejected,
				"resume_identity_mismatch",
				"Claude resumed a different native session",
				nil,
			))
		}
		if !sameDirectory(init.System.CWD, options.Cwd) {
			p.finishOpen()
			return nil, closeAfterOpenFailure(native, adapterError(
				agent.ErrorNativeRejected,
				"session_cwd_mismatch",
				"Claude init working directory does not match the requested directory",
				nil,
			))
		}
	}
	if resume != nil {
		if err := resume.target.verifyPostcheck(ctx); err != nil {
			p.finishOpen()
			return nil, closeAfterOpenFailure(native, err)
		}
	}
	created, err := newSession(
		p,
		native,
		init,
		sessionIdentity(init, resume),
		options.Features,
		initialExecutionOptionsState(init, options.Execution),
		buffered,
		guard,
	)
	if err != nil {
		p.finishOpen()
		return nil, closeAfterOpenFailure(native, err)
	}
	p.completeOpen(created)
	created.start()
	return created, nil
}

func sessionIdentity(
	init *claudetypes.Event,
	resume *resumeAttempt,
) string {
	if init != nil {
		return init.SessionID
	}
	if resume != nil {
		return resume.target.sessionID
	}
	return ""
}

func (p *Provider) validateResumeConfig() error {
	switch {
	case p.config.ConfigDir == "":
		return adapterError(
			agent.ErrorFailedPrecondition,
			"resume_config_dir_required",
			"Claude resume requires an explicit configuration directory",
			nil,
		)
	case !filepath.IsAbs(p.config.ConfigDir):
		return adapterError(
			agent.ErrorFailedPrecondition,
			"resume_config_dir_not_absolute",
			"Claude resume configuration directory must be absolute",
			nil,
		)
	default:
		return nil
	}
}

func (p *Provider) reserveOpen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch p.state {
	case providerStarted:
		p.opening++
		return nil
	case providerStopped:
		return adapterError(
			agent.ErrorClosed,
			"provider_stopped",
			"provider has been stopped",
			nil,
		)
	default:
		return adapterError(
			agent.ErrorFailedPrecondition,
			"provider_not_started",
			"provider must be started before opening a session",
			nil,
		)
	}
}

func (p *Provider) finishOpen() {
	p.mu.Lock()
	p.opening--
	p.mu.Unlock()
}

func (p *Provider) completeOpen(created *session) {
	p.mu.Lock()
	p.opening--
	if p.sessions == nil {
		p.sessions = make(map[*session]struct{})
	}
	p.sessions[created] = struct{}{}
	p.mu.Unlock()
}

func (p *Provider) sessionClosed(closed *session) {
	p.mu.Lock()
	delete(p.sessions, closed)
	p.mu.Unlock()
}

func providerCapabilities() agent.Capabilities {
	return agent.Capabilities{
		agent.CapabilityTextInput:  true,
		agent.CapabilityPermission: true,
		agent.CapabilityForm:       true,
	}
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

func (p *Provider) nativeOptions(
	workDir string,
	resumeID string,
	features agent.Features,
	mcpServers []claude.MCPServer,
	execution agent.ExecutionOptions,
) claude.Options {
	config := p.config
	options := claude.Options{
		CLIPath:           config.CLIPath,
		WorkDir:           workDir,
		MCPServers:        mcpServers,
		Model:             execution.Model,
		Effort:            execution.ReasoningEffort,
		Initialize:        config.Initialize,
		ResumeID:          resumeID,
		ChildDetailPolicy: config.ChildDetailPolicy,
		Subagents:         config.Subagents,
		Env:               config.Env,
		InitializeTimeout: config.InitializeTimeout,
		ShutdownTimeout:   config.ShutdownTimeout,
		Limits:            config.Limits,
	}
	if !features.AgentQuestionsEnabled() {
		options.DisallowedTools = []string{"AskUserQuestion"}
	}
	return options
}

func validateOpenOptions(options agent.OpenOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if options.SystemPrompt != "" {
		return adapterError(
			agent.ErrorUnsupported,
			"system_prompt_unsupported",
			"Claude system prompts are not supported by this adapter",
			nil,
		)
	}
	if len(options.AdditionalDirs) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			"additional_dirs_unsupported",
			"Claude additional directories are not supported by this adapter",
			nil,
		)
	}
	return nil
}

func waitForInit(
	ctx context.Context,
	native *claude.Session,
) (*claudetypes.Event, []claude.Received, error) {
	var buffered initBuffer
	for {
		received, err := native.Receive(ctx)
		if err != nil {
			return nil, nil, adapterError(
				agent.ErrorUnavailable,
				"initialize_wait_failed",
				"wait for Claude init",
				err,
			)
		}
		if received.Event == nil {
			if received.Request != nil {
				if err := buffered.add(received); err != nil {
					return nil, nil, err
				}
			}
			continue
		}
		if received.Event.System != nil && received.Event.Subtype == "init" {
			if received.Event.SessionID == "" {
				return nil, nil, adapterError(
					agent.ErrorUnavailable,
					"initialize_identity_missing",
					"Claude init has no session identity",
					nil,
				)
			}
			if received.Event.IsReplay != nil && *received.Event.IsReplay {
				return nil, nil, adapterError(
					agent.ErrorNativeRejected,
					"initialize_replayed",
					"Claude returned replay material instead of a live init",
					nil,
				)
			}
			event := *received.Event
			return &event, buffered.events, nil
		}
		if err := buffered.add(received); err != nil {
			return nil, nil, err
		}
	}
}

func waitForInitContext(
	ctx context.Context,
	timeout time.Duration,
) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultInitializeWait
	}
	return context.WithTimeout(ctx, timeout)
}

type initBuffer struct {
	events []claude.Received
	bytes  int
}

func (b *initBuffer) add(received claude.Received) error {
	size := len(received.Raw)
	if received.Event != nil {
		size = max(size, len(received.Event.Raw))
	}
	if len(b.events) >= maxInitBufferedEvents ||
		size > maxInitBufferedBytes-b.bytes {
		return adapterError(
			agent.ErrorResourceExhausted,
			"initialize_buffer_exhausted",
			"Claude init staging exceeded its limit",
			nil,
		)
	}
	b.events = append(b.events, received)
	b.bytes += size
	return nil
}

func closeAfterOpenFailure(native *claude.Session, cause error) error {
	closeErr := native.Close()
	if closeErr == nil {
		return cause
	}
	return errors.Join(cause, nativeOperationError("close", closeErr))
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil || !leftInfo.IsDir() || !rightInfo.IsDir() {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}
