package agentclaude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
)

type resumeAttempt struct {
	target *resumeTarget
}

type resumeTarget struct {
	configDir     string
	sessionID     string
	cwd           string
	path          string
	canonicalPath string
	info          os.FileInfo
}

type resumeIdentityGuard struct {
	target   *resumeTarget
	initSeen bool
}

func (p *Core) prepareResume(
	ctx context.Context,
	options agent.ResumeOptions,
	env []string,
) (*resumeAttempt, error) {
	if err := p.requireResumeVersion(ctx, options.Cwd, env); err != nil {
		return nil, err
	}
	target, err := p.preflightResume(ctx, options)
	if err != nil {
		return nil, err
	}
	return &resumeAttempt{target: target}, nil
}

func (p *Core) requireResumeVersion(ctx context.Context, cwd string, env []string) error {
	cliPath := p.config.CLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	_, err := claude.CheckCLIVersion(ctx, claude.Options{CLIPath: cliPath, WorkDir: cwd, Env: env})
	if err == nil {
		return nil
	}
	var versionErr *claude.CLIVersionError
	if errors.As(err, &versionErr) {
		reason := ErrorReasonResumeVersionUnsupported
		if versionErr.Version == "" {
			reason = ErrorReasonResumeVersionUnconfirmed
		}
		return adapterError(
			agent.ErrorUnsupported,
			reason,
			"Claude resume requires stable CLI >= "+claude.MinimumCLIVersion,
			err,
		)
	}
	return adapterError(
		agent.ErrorUnavailable,
		ErrorReasonResumeVersionQueryFailed,
		"query Claude version before resume",
		err,
	)
}

func (p *Core) preflightResume(
	ctx context.Context,
	options agent.ResumeOptions,
) (*resumeTarget, error) {
	result, err := queryResumeInfo(
		ctx,
		p.config.ConfigDir,
		options.SessionID,
		options.Cwd,
	)
	if err != nil {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightQueryFailed,
			"Claude saved-session metadata could not be verified",
			err,
		)
	}
	if result.Info == nil {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightMissing,
			"Claude saved session was not found",
			nil,
		)
	}
	if result.Coverage.Status != "complete" {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightIncomplete,
			"Claude saved-session metadata coverage is incomplete",
			nil,
		)
	}
	info := result.Info
	if info.SessionID != options.SessionID {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightIdentityMismatch,
			"Claude saved-session identity does not match the requested session",
			nil,
		)
	}
	if info.Cwd == nil || !sameDirectory(*info.Cwd, options.Cwd) {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightCwdMismatch,
			"Claude saved-session working directory does not match the request",
			nil,
		)
	}
	fileInfo, canonicalPath, err := regularTranscriptInfo(
		p.config.ConfigDir,
		info.Path,
	)
	if err != nil {
		return nil, resumePreflightError(
			ErrorReasonResumePreflightFileInvalid,
			"Claude saved transcript is not a regular file under the configured directory",
			err,
		)
	}
	return &resumeTarget{
		configDir:     p.config.ConfigDir,
		sessionID:     info.SessionID,
		cwd:           options.Cwd,
		path:          filepath.Clean(info.Path),
		canonicalPath: canonicalPath,
		info:          fileInfo,
	}, nil
}

func queryResumeInfo(
	ctx context.Context,
	configDir string,
	sessionID string,
	cwd string,
) (claude.SessionInfoResult, error) {
	return claude.GetSessionInfo(
		ctx,
		sessionID,
		claude.SessionInfoOptions{
			Scope: claude.SessionQueryScope{ConfigDir: configDir, ProjectDir: cwd},
		},
	)
}

func resumePreflightError(reason, message string, cause error) error {
	return adapterError(
		agent.ErrorFailedPrecondition,
		reason,
		message,
		cause,
	)
}

func (t *resumeTarget) verifyPostcheck(ctx context.Context) error {
	result, err := queryResumeInfo(ctx, t.configDir, t.sessionID, t.cwd)
	if err != nil {
		return resumePostcheckError(
			ErrorReasonResumePostcheckQueryFailed,
			"Claude saved-session metadata changed after identity confirmation",
			err,
		)
	}
	if result.Info == nil ||
		result.Coverage.Status != "complete" ||
		result.Info.SessionID != t.sessionID ||
		filepath.Clean(result.Info.Path) != t.path ||
		result.Info.Cwd == nil ||
		!sameDirectory(*result.Info.Cwd, t.cwd) {
		return resumePostcheckError(
			ErrorReasonResumePostcheckMismatch,
			"Claude saved-session metadata no longer matches the confirmed identity",
			nil,
		)
	}
	currentInfo, canonicalPath, err := regularTranscriptInfo(
		t.configDir,
		result.Info.Path,
	)
	if err != nil ||
		canonicalPath != t.canonicalPath ||
		!os.SameFile(t.info, currentInfo) {
		return resumePostcheckError(
			ErrorReasonResumePostcheckFileChanged,
			"Claude saved transcript changed after identity confirmation",
			err,
		)
	}
	return nil
}

func resumePostcheckError(reason, message string, cause error) error {
	return adapterError(
		agent.ErrorNativeRejected,
		reason,
		message,
		cause,
	)
}

func regularTranscriptInfo(configDir, path string) (os.FileInfo, string, error) {
	if !filepath.IsAbs(configDir) || !filepath.IsAbs(path) {
		return nil, "", errors.New("absolute config and transcript paths required")
	}
	lexicalRoot := filepath.Clean(configDir)
	lexicalPath := filepath.Clean(path)
	if !pathWithin(lexicalRoot, lexicalPath) {
		return nil, "", errors.New("transcript is outside the configured directory")
	}
	entry, err := os.Lstat(lexicalPath)
	if err != nil {
		return nil, "", err
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("transcript is not a regular non-symlink file")
	}
	opened, err := os.Stat(lexicalPath)
	if err != nil {
		return nil, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return nil, "", errors.New("transcript identity changed while opening")
	}
	canonicalRoot, err := canonicalPath(lexicalRoot)
	if err != nil {
		return nil, "", err
	}
	canonicalFile, err := canonicalPath(lexicalPath)
	if err != nil {
		return nil, "", err
	}
	if !pathWithin(canonicalRoot, canonicalFile) {
		return nil, "", errors.New("transcript resolves outside the configured directory")
	}
	return opened, canonicalFile, nil
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("absolute path required")
	}
	return filepath.EvalSymlinks(filepath.Clean(path))
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func waitForResumeIdentity(
	ctx context.Context,
	native *claude.Session,
	target *resumeTarget,
) (*claude.Event, []claude.Received, *resumeIdentityGuard, error) {
	var buffered initBuffer
	var init *claude.Event
	guard := &resumeIdentityGuard{target: target}
	for {
		received, err := native.Receive(ctx)
		if err != nil {
			reason := ErrorReasonResumeIdentityWaitFailed
			message := "wait for Claude resume identity"
			if ctx.Err() != nil {
				reason = ErrorReasonResumeIdentityTimeout
				message = "Claude did not confirm resume identity before the timeout"
				err = ctx.Err()
			}
			return nil, nil, nil, adapterError(
				agent.ErrorUnavailable,
				reason,
				message,
				err,
			)
		}
		if received.Identity != nil {
			identity := received.Identity
			if identity.Err != nil {
				code, reason := agent.ErrorUnavailable, ErrorReasonResumeIdentityWaitFailed
				if errors.Is(identity.Err, claude.ErrProtocol) {
					code, reason = agent.ErrorNativeRejected, ErrorReasonResumeIdentityMismatch
				} else if errors.Is(identity.Err, context.DeadlineExceeded) || errors.Is(identity.Err, context.Canceled) {
					reason = ErrorReasonResumeIdentityTimeout
				}
				return nil, nil, nil, adapterError(code, reason, "Claude read-only identity probe failed", nil)
			}
			if identity.SessionID != target.sessionID || !sameDirectory(identity.Cwd, target.cwd) {
				return nil, nil, nil, adapterError(agent.ErrorNativeRejected, ErrorReasonResumeIdentityMismatch, "Claude status identity conflicts with the saved-session target", nil)
			}
			return init, buffered.events, guard, nil
		}
		if received.Event == nil {
			if received.Request != nil {
				if err := buffered.add(received); err != nil {
					return nil, nil, nil, err
				}
			}
			continue
		}

		if isResumeInit(received.Event) {
			if err := target.verifyInit(received.Event); err != nil {
				return nil, nil, nil, err
			}
			if guard.initSeen {
				return nil, nil, nil, resumeDuplicateInit()
			}
			guard.initSeen = true
			event := *received.Event
			init = &event
			continue
		}
		if err := buffered.add(received); err != nil {
			return nil, nil, nil, err
		}
	}
}

func isResumeInit(event *claude.Event) bool {
	return event != nil &&
		event.Type == "system" &&
		event.Subtype == "init"
}

func (t *resumeTarget) verifyInit(event *claude.Event) error {
	if event.IsReplay != nil && *event.IsReplay {
		return adapterError(
			agent.ErrorNativeRejected,
			ErrorReasonResumeInitReplayed,
			"Claude returned replay material instead of a live init",
			nil,
		)
	}
	if event.SessionID == "" || event.SessionID != t.sessionID ||
		event.System == nil ||
		!sameDirectory(event.System.CWD, t.cwd) {
		return adapterError(
			agent.ErrorNativeRejected,
			ErrorReasonResumeIdentityConflict,
			"Claude init identity conflicts with the saved-session target",
			nil,
		)
	}
	return nil
}

func (g *resumeIdentityGuard) inspect(event *claude.Event) (bool, error) {
	if g == nil || !isResumeInit(event) {
		return false, nil
	}
	if err := g.target.verifyInit(event); err != nil {
		return true, err
	}
	if g.initSeen {
		return true, resumeDuplicateInit()
	}
	g.initSeen = true
	return false, nil
}

func resumeDuplicateInit() error {
	return adapterError(
		agent.ErrorNativeRejected,
		ErrorReasonResumeInitDuplicate,
		"Claude resume emitted init identity more than once",
		nil,
	)
}
