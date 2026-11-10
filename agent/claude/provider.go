package agentclaude

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/lifecycle"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)

var _ agent.Provider = (*Provider)(nil)

const (
	defaultInitializeWait = 60 * time.Second
	maxInitBufferedEvents = 128
	maxInitBufferedBytes  = 4 << 20
)

// Provider owns the lifecycle of the Claude sessions it creates. Each Claude
// session owns its native CLI process.
type Provider struct {
	config    Config
	streams   streamFactory
	lifecycle *lifecycle.Lifecycle[struct{}, *session]
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
	provider := &Provider{config: cloneConfig(config)}
	provider.lifecycle = lifecycle.New[struct{}, *session](
		providerCapabilities(),
		nil,
		nil,
	)
	return provider
}

// Start marks this provider ready to open sessions.
func (p *Provider) Start(ctx context.Context) (agent.Capabilities, error) {
	return p.lifecycle.Start(ctx)
}

// Stop rejects shutdown while any session is opening or open.
func (p *Provider) Stop(ctx context.Context) error {
	return p.lifecycle.Stop(ctx)
}

// Open creates a new Claude session after native initialization confirms its
// real identity.
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
	mcpServers, err := claudeMCPServers(options.MCPServers)
	if err != nil {
		return nil, err
	}
	return p.openLifecycle(ctx, func(ctx context.Context) (*session, error) {
		return p.openNativeSession(ctx, options, "", nil, mcpServers)
	})
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
			ErrorReasonNilContext,
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
	return p.openLifecycle(ctx, func(ctx context.Context) (*session, error) {
		attempt, err := p.prepareResume(ctx, options)
		if err != nil {
			return nil, err
		}
		return p.openNativeSession(
			ctx,
			options.OpenOptions,
			options.SessionID,
			attempt,
			mcpServers,
		)
	})
}

func (p *Provider) openLifecycle(
	ctx context.Context,
	open func(context.Context) (*session, error),
) (agent.Session, error) {
	var created *session
	err := p.lifecycle.Open(ctx, func(
		ctx context.Context,
		_ struct{},
	) (*session, error) {
		opened, err := open(ctx)
		if err != nil {
			return nil, err
		}
		created = opened
		return opened, nil
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

func (p *Provider) openNativeSession(
	ctx context.Context,
	options agent.OpenOptions,
	resumeID string,
	resume *resumeAttempt,
	mcpServers []claude.MCPServer,
) (*session, error) {
	nativeOptions := p.nativeOptions(
		options.Cwd,
		resumeID,
		options.Features,
		mcpServers,
		options.Execution,
	)
	nativeOptions.AppendSystemPrompt = options.AdditionalSystemPrompt
	if resume != nil {
		nativeOptions.SessionStartProbe = &resume.probe
	}
	var stream *streamOpen
	if p.streams != nil {
		opened, err := p.streams(ctx, claude.SessionOptions(nativeOptions))
		if err != nil {
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
		return nil, closeAfterOpenFailure(native, err)
	}
	if init != nil {
		if resumeID != "" && init.SessionID != resumeID {
			return nil, closeAfterOpenFailure(native, adapterError(
				agent.ErrorNativeRejected,
				ErrorReasonResumeIdentityMismatch,
				"Claude resumed a different native session",
				nil,
			))
		}
		if !sameDirectory(init.System.CWD, options.Cwd) {
			return nil, closeAfterOpenFailure(native, adapterError(
				agent.ErrorNativeRejected,
				ErrorReasonSessionCWDMismatch,
				"Claude init working directory does not match the requested directory",
				nil,
			))
		}
	}
	if resume != nil {
		if err := resume.target.verifyPostcheck(ctx); err != nil {
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
		return nil, closeAfterOpenFailure(native, err)
	}
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
			ErrorReasonResumeConfigDirRequired,
			"Claude resume requires an explicit configuration directory",
			nil,
		)
	case !filepath.IsAbs(p.config.ConfigDir):
		return adapterError(
			agent.ErrorFailedPrecondition,
			ErrorReasonResumeConfigDirNotAbsolute,
			"Claude resume configuration directory must be absolute",
			nil,
		)
	default:
		return nil
	}
}

func (p *Provider) sessionClosed(closed *session) {
	p.lifecycle.SessionClosed(closed)
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
	if len(options.AdditionalDirs) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonAdditionalDirsUnsupported,
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
				ErrorReasonInitializeWaitFailed,
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
					ErrorReasonInitializeIdentityMissing,
					"Claude init has no session identity",
					nil,
				)
			}
			if received.Event.IsReplay != nil && *received.Event.IsReplay {
				return nil, nil, adapterError(
					agent.ErrorNativeRejected,
					ErrorReasonInitializeReplayed,
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
			ErrorReasonInitializeBufferExhausted,
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
