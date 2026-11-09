package agentclaude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/uuidv4"
)

const (
	claudeResumeVersion       = "2.1.258"
	claudeResumeProbePrefix   = "agent-sdk-go:claude:resume-probe:"
	claudeVersionQueryTimeout = 10 * time.Second
	claudeVersionOutputLimit  = 4 << 10
)

var claudeVersionPattern = regexp.MustCompile(`(?i)v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?`)

type resumeAttempt struct {
	target *resumeTarget
	probe  claude.SessionStartProbe
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
	target              *resumeTarget
	callbackID          string
	callbackWasIdentity bool
	lateCallbackSeen    bool
	lateInitSeen        bool
}

type resumeCallbackInput struct {
	HookEventName  string `json:"hook_event_name"`
	Source         string `json:"source"`
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
}

func (p *Provider) prepareResume(
	ctx context.Context,
	options agent.ResumeOptions,
) (*resumeAttempt, error) {
	if err := p.requireResumeVersion(ctx, options.Cwd); err != nil {
		return nil, err
	}
	target, err := p.preflightResume(ctx, options)
	if err != nil {
		return nil, err
	}
	callbackID, err := uuidv4.New()
	if err != nil {
		return nil, adapterError(
			agent.ErrorUnavailable,
			"resume_probe_id_failed",
			"create Claude resume probe identity",
			err,
		)
	}
	return &resumeAttempt{
		target: target,
		probe: claude.SessionStartProbe{
			Source:     "resume",
			CallbackID: claudeResumeProbePrefix + callbackID,
		},
	}, nil
}

func (p *Provider) requireResumeVersion(ctx context.Context, cwd string) error {
	queryCtx, cancel := context.WithTimeout(ctx, claudeVersionQueryTimeout)
	defer cancel()

	cliPath := p.config.CLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	var output cappedOutput
	output.limit = claudeVersionOutputLimit
	command := exec.CommandContext(queryCtx, cliPath, "--version")
	command.Dir = cwd
	command.Env = p.config.Env
	command.Stdout = &output
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		return adapterError(
			agent.ErrorUnavailable,
			"resume_version_query_failed",
			"query Claude version before resume",
			err,
		)
	}
	if output.truncated {
		return adapterError(
			agent.ErrorUnsupported,
			"resume_version_unconfirmed",
			"Claude version output exceeded its review bound",
			nil,
		)
	}
	versions := claudeVersionTokens(string(output.data))
	if len(versions) == 0 {
		return adapterError(
			agent.ErrorUnsupported,
			"resume_version_unconfirmed",
			"Claude version could not be confirmed",
			nil,
		)
	}
	for _, version := range versions {
		if version != claudeResumeVersion {
			return adapterError(
				agent.ErrorUnsupported,
				"resume_version_unsupported",
				"Claude resume requires CLI "+claudeResumeVersion,
				nil,
			)
		}
	}
	return nil
}

func claudeVersionTokens(output string) []string {
	matches := claudeVersionPattern.FindAllStringIndex(output, -1)
	versions := make([]string, 0, len(matches))
	for _, match := range matches {
		if !versionTokenBoundary(output, match[0], match[1]) {
			continue
		}
		version := output[match[0]:match[1]]
		if version[0] == 'v' || version[0] == 'V' {
			version = version[1:]
		}
		versions = append(versions, version)
	}
	return versions
}

func versionTokenBoundary(output string, start, end int) bool {
	if start > 0 && isVersionTokenByte(output[start-1]) {
		return false
	}
	return end == len(output) || !isVersionTokenByte(output[end])
}

func isVersionTokenByte(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value == '.' || value == '-' || value == '+' || value == '_'
}

type cappedOutput struct {
	data      []byte
	limit     int
	truncated bool
}

func (o *cappedOutput) Write(data []byte) (int, error) {
	remaining := o.limit - len(o.data)
	if remaining > 0 {
		o.data = append(o.data, data[:min(len(data), remaining)]...)
	}
	if len(data) > remaining {
		o.truncated = true
	}
	return len(data), nil
}

func (p *Provider) preflightResume(
	ctx context.Context,
	options agent.ResumeOptions,
) (*resumeTarget, error) {
	result, err := queryResumeInfo(
		ctx,
		p.config.ConfigDir,
		options.SessionID,
	)
	if err != nil {
		return nil, resumePreflightError(
			"resume_preflight_query_failed",
			"Claude saved-session metadata could not be verified",
			err,
		)
	}
	if result.Info == nil {
		return nil, resumePreflightError(
			"resume_preflight_missing",
			"Claude saved session was not found",
			nil,
		)
	}
	if result.Coverage.Status != "complete" {
		return nil, resumePreflightError(
			"resume_preflight_incomplete",
			"Claude saved-session metadata coverage is incomplete",
			nil,
		)
	}
	info := result.Info
	if info.SessionID != options.SessionID {
		return nil, resumePreflightError(
			"resume_preflight_identity_mismatch",
			"Claude saved-session identity does not match the requested session",
			nil,
		)
	}
	if info.Cwd == nil || !sameDirectory(*info.Cwd, options.Cwd) {
		return nil, resumePreflightError(
			"resume_preflight_cwd_mismatch",
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
			"resume_preflight_file_invalid",
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
) (claude.SessionInfoResult, error) {
	return claude.GetSessionInfo(
		ctx,
		sessionID,
		claude.SessionInfoOptions{
			Scope: claude.SessionQueryScope{ConfigDir: configDir},
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
	result, err := queryResumeInfo(ctx, t.configDir, t.sessionID)
	if err != nil {
		return resumePostcheckError(
			"resume_postcheck_query_failed",
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
			"resume_postcheck_mismatch",
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
			"resume_postcheck_file_changed",
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
		return nil, "", fmt.Errorf("absolute config and transcript paths required")
	}
	lexicalRoot := filepath.Clean(configDir)
	lexicalPath := filepath.Clean(path)
	if !pathWithin(lexicalRoot, lexicalPath) {
		return nil, "", fmt.Errorf("transcript is outside the configured directory")
	}
	entry, err := os.Lstat(lexicalPath)
	if err != nil {
		return nil, "", err
	}
	if !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 {
		return nil, "", fmt.Errorf("transcript is not a regular non-symlink file")
	}
	opened, err := os.Stat(lexicalPath)
	if err != nil {
		return nil, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return nil, "", fmt.Errorf("transcript identity changed while opening")
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
		return nil, "", fmt.Errorf("transcript resolves outside the configured directory")
	}
	return opened, canonicalFile, nil
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
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
	callbackID string,
) (*claude.Event, []claude.Received, *resumeIdentityGuard, error) {
	var buffered initBuffer
	for {
		received, err := native.Receive(ctx)
		if err != nil {
			reason := "resume_identity_wait_failed"
			message := "wait for Claude resume identity"
			if ctx.Err() != nil {
				reason = "resume_identity_timeout"
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
		if received.Event == nil {
			if received.Request != nil {
				if err := buffered.add(received); err != nil {
					return nil, nil, nil, err
				}
			}
			continue
		}

		owned, callbackErr := target.verifyCallback(received.Event, callbackID)
		if owned {
			if callbackErr != nil {
				return nil, nil, nil, callbackErr
			}
			return nil, buffered.events, &resumeIdentityGuard{
				target:              target,
				callbackID:          callbackID,
				callbackWasIdentity: true,
			}, nil
		}
		if isResumeInit(received.Event) {
			if err := target.verifyInit(received.Event); err != nil {
				return nil, nil, nil, err
			}
			event := *received.Event
			return &event, buffered.events, &resumeIdentityGuard{
				target:     target,
				callbackID: callbackID,
			}, nil
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
			"resume_init_replayed",
			"Claude returned replay material instead of a live init",
			nil,
		)
	}
	if event.SessionID == "" || event.SessionID != t.sessionID ||
		event.System == nil ||
		!sameDirectory(event.System.CWD, t.cwd) {
		return adapterError(
			agent.ErrorNativeRejected,
			"resume_identity_conflict",
			"Claude init identity conflicts with the saved-session target",
			nil,
		)
	}
	return nil
}

func (t *resumeTarget) verifyCallback(
	event *claude.Event,
	callbackID string,
) (bool, error) {
	if event == nil ||
		event.Control == nil ||
		event.Control.Subtype != "hook_callback" {
		return false, nil
	}
	var request struct {
		CallbackID json.RawMessage `json:"callback_id"`
		Input      json.RawMessage `json:"input"`
	}
	if json.Unmarshal(event.Control.Request, &request) != nil {
		return false, nil
	}
	var actualID string
	if json.Unmarshal(request.CallbackID, &actualID) != nil ||
		actualID != callbackID {
		return false, nil
	}

	var input resumeCallbackInput
	if len(request.Input) == 0 ||
		json.Unmarshal(request.Input, &input) != nil ||
		input.HookEventName != "SessionStart" ||
		input.Source != "resume" ||
		input.SessionID != t.sessionID ||
		!sameDirectory(input.CWD, t.cwd) ||
		!t.transcriptMatches(input.TranscriptPath) {
		return true, adapterError(
			agent.ErrorNativeRejected,
			"resume_callback_identity_mismatch",
			"Claude SessionStart callback does not match the saved-session target",
			nil,
		)
	}
	return true, nil
}

func (t *resumeTarget) transcriptMatches(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	canonical, err := canonicalPath(path)
	if err != nil || canonical != t.canonicalPath {
		return false
	}
	info, err := os.Stat(path)
	return err == nil &&
		info.Mode().IsRegular() &&
		os.SameFile(t.info, info)
}

func (g *resumeIdentityGuard) inspect(
	event *claude.Event,
) (bool, error) {
	if g == nil {
		return false, nil
	}
	owned, callbackErr := g.target.verifyCallback(event, g.callbackID)
	if owned {
		if callbackErr != nil {
			return true, callbackErr
		}
		if g.callbackWasIdentity || g.lateCallbackSeen {
			return true, adapterError(
				agent.ErrorNativeRejected,
				"resume_callback_duplicate",
				"Claude SessionStart identity callback arrived more than once",
				nil,
			)
		}
		g.lateCallbackSeen = true
		return true, nil
	}
	if !isResumeInit(event) {
		return false, nil
	}
	if err := g.target.verifyInit(event); err != nil {
		return true, err
	}
	if !g.callbackWasIdentity {
		return true, resumeDuplicateInit()
	}
	if g.lateInitSeen {
		return true, resumeDuplicateInit()
	}
	g.lateInitSeen = true
	return true, nil
}

func resumeDuplicateInit() error {
	return adapterError(
		agent.ErrorNativeRejected,
		"resume_init_duplicate",
		"Claude resume emitted init identity more than once",
		nil,
	)
}
