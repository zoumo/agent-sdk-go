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
	"github.com/zoumo/agent-sdk-go/agent/internal/corelease"
	"github.com/zoumo/agent-sdk-go/claude"
	claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/mcp"
)

var _ agent.Core = (*Core)(nil)

const (
	defaultInitializeWait = 60 * time.Second
	maxInitBufferedEvents = 128
	maxInitBufferedBytes  = 4 << 20
)

// Core owns the lifecycle of the Claude sessions it creates. Each Claude
// session owns its native CLI process.
type Core struct {
	config       Config
	streams      streamFactory
	newSessionID func() (string, error)
	leases       *corelease.Core[struct{}, *session]
}

type streamOpen struct {
	Transport claude.StreamTransport
	Workspace string
	Env       []string
	Started   func(*claude.Session)
}

type streamFactory func(context.Context, claude.SessionOptions) (streamOpen, error)

// New constructs a Claude Core without starting a native process.
func New(config Config) *Core {
	core := &Core{config: cloneConfig(config)}
	core.leases = corelease.New[struct{}, *session](
		providerCapabilities(),
		nil,
		nil,
	)
	return core
}

// Start marks this Core ready to open sessions.
func (p *Core) Start(ctx context.Context) (agent.Capabilities, error) {
	return p.leases.Start(ctx)
}

// Stop rejects shutdown while any session is opening or open.
func (p *Core) Stop(ctx context.Context) error {
	return p.leases.Stop(ctx)
}

// Open creates a new Claude session and binds it to the claimed working
// directory before returning. The CLI assigns the native identity, so the
// returned Session is usable before any user input.
func (p *Core) Open(
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
	env := effectiveProcessEnvironment(p.config.Env, options.ProcessEnv)
	return p.openLifecycle(ctx, func(ctx context.Context) (*session, error) {
		if p.newSessionID == nil {
			return p.openNativeSession(ctx, options, "", "", nil, mcpServers, env, true)
		}
		sessionID, err := p.newSessionID()
		if err != nil || sessionID == "" {
			return nil, adapterError(
				agent.ErrorUnavailable,
				ErrorReasonSessionIDFailed,
				"create Claude native session identity",
				err,
			)
		}
		return p.openNativeSession(ctx, options, "", sessionID, nil, mcpServers, env, false)
	})
}

// Resume confirms the supported CLI version and saved transcript before launch,
// then restores the requested session after live init or its private callback
// confirms the same identity.
func (p *Core) Resume(
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
	env := effectiveProcessEnvironment(p.config.Env, options.ProcessEnv)
	return p.openLifecycle(ctx, func(ctx context.Context) (*session, error) {
		attempt, err := p.prepareResume(ctx, options, env)
		if err != nil {
			return nil, err
		}
		return p.openNativeSession(
			ctx,
			options.OpenOptions,
			options.SessionID,
			"",
			attempt,
			mcpServers,
			env,
			false,
		)
	})
}

func (p *Core) openLifecycle(
	ctx context.Context,
	open func(context.Context) (*session, error),
) (agent.Session, error) {
	created, err := p.leases.Open(ctx, func(
		ctx context.Context,
		_ struct{},
	) (*session, error) {
		return open(ctx)
	})
	if err != nil {
		return nil, err
	}
	created.start()
	return created, nil
}

func (p *Core) openNativeSession(
	ctx context.Context,
	options agent.OpenOptions,
	resumeID string,
	sessionID string,
	resume *resumeAttempt,
	mcpServers []mcp.Server,
	env []string,
	claim bool,
) (*session, error) {
	nativeOptions := p.nativeOptions(
		options.Cwd,
		resumeID,
		options.Features,
		mcpServers,
		options.Config,
	)
	nativeOptions.SessionID = sessionID
	nativeOptions.ClaimSession = claim
	nativeOptions.Env = slices.Clone(env)
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
	var (
		init     *claudetypes.Event
		buffered []claude.Received
		guard    *resumeIdentityGuard
	)
	if resume != nil {
		initContext, cancelInit := waitForInitContext(ctx, p.config.InitializeTimeout)
		init, buffered, guard, err = waitForResumeIdentity(
			initContext,
			native,
			resume.target,
			resume.probe.CallbackID,
		)
		cancelInit()
	}
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
	identity := sessionID
	if resume != nil {
		identity = resume.target.sessionID
	}
	if identity == "" && native != nil {
		identity, _ = native.SessionID()
	}
	created, err := newSession(
		p,
		native,
		init,
		identity,
		options.Features,
		buffered,
		guard,
	)
	if err != nil {
		return nil, closeAfterOpenFailure(native, err)
	}
	return created, nil
}

func (p *Core) validateResumeConfig() error {
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

func (p *Core) sessionClosed(closed *session) {
	p.leases.SessionClosed(closed)
}

func providerCapabilities() agent.Capabilities {
	return agent.Capabilities{
		agent.CapabilityProcessEnv:  true,
		agent.CapabilityTextInput:   true,
		agent.CapabilityPromptTrace: true,
		agent.CapabilityPermission:  true,
		agent.CapabilityForm:        true,
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

func (p *Core) nativeOptions(
	workDir string,
	resumeID string,
	features agent.Features,
	mcpServers []mcp.Server,
	sessionConfig agent.SessionConfig,
) claude.Options {
	config := p.config
	options := claude.Options{
		CLIPath:           config.CLIPath,
		WorkDir:           workDir,
		MCPServers:        mcpServers,
		Model:             sessionConfig.Model,
		Effort:            sessionConfig.Effort,
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
	if features.RestrictLocalTools {
		options.DisallowedTools = append(options.DisallowedTools,
			"Bash", "PowerShell", "Read", "Glob", "Grep", "Write", "Edit",
			"NotebookEdit", "EnterWorktree", "ExitWorktree",
		)
	}
	return options
}

func validateOpenOptions(options agent.OpenOptions) error {
	if len(options.ShellEnv) != 0 {
		return adapterError(
			agent.ErrorUnsupported,
			agent.ErrorReasonShellEnvUnsupported,
			"Claude Session shell environment is not supported",
			nil,
		)
	}
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
