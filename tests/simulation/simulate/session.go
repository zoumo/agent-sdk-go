package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	preparedSessionSchema = 3
	lifecycleTimeout      = 30 * time.Second
)

type preparedConnection struct {
	ID       string   `json:"id"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Launcher string   `json:"launcher,omitempty"`
}

type preparedRoute struct {
	Enabled     bool                 `json:"enabled"`
	Executor    string               `json:"executor"`
	Target      string               `json:"target"`
	Provenance  string               `json:"provenance"`
	Workspace   string               `json:"workspace"`
	ProtocolCwd string               `json:"protocolCwd"`
	Env         map[string]string    `json:"env"`
	Connections []preparedConnection `json:"connections"`
}

type preparedSource struct {
	Kind          string `json:"kind"`
	ID            string `json:"id,omitempty"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	Provenance    string `json:"provenance"`
	DriverProfile string `json:"driverProfile,omitempty"`
}

type preparedSession struct {
	SchemaVersion int                      `json:"schemaVersion"`
	SessionID     string                   `json:"sessionId"`
	Tool          command                  `json:"tool"`
	ToolVersion   string                   `json:"toolVersion"`
	ToolSHA256    string                   `json:"toolSHA256"`
	AgentType     string                   `json:"agentType"`
	Source        preparedSource           `json:"source"`
	ResourceRoot  string                   `json:"resourceRoot"`
	Workspace     string                   `json:"workspace"`
	Home          string                   `json:"home"`
	Config        string                   `json:"config"`
	Tmp           string                   `json:"tmp"`
	ProtocolCwd   string                   `json:"protocolCwd"`
	Env           map[string]string        `json:"env"`
	Routes        map[string]preparedRoute `json:"routes"`

	Dir     string        `json:"-"`
	RawJSON []byte        `json:"-"`
	Mock    preparedRoute `json:"-"`
}

type sourceSelection struct {
	Provider  string
	Case      string
	Recording string
}

func prepareSession(ctx context.Context, tool toolIdentity, source sourceSelection, dir, policy string) (session *preparedSession, err error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("prepared session directory must be absolute")
	}
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		if statErr == nil {
			return nil, fmt.Errorf("prepared session directory already exists: %s", dir)
		}
		return nil, statErr
	}
	args := []string{"prepare"}
	switch {
	case source.Recording != "":
		if !filepath.IsAbs(source.Recording) {
			return nil, errors.New("--recording must be absolute")
		}
		args = append(args, "--recording", source.Recording)
	case source.Case != "":
		args = append(args, "--agent", source.Provider, "--case", source.Case)
	default:
		return nil, errors.New("prepare requires --case with --provider or --recording")
	}
	args = append(args, "--session-dir", dir)
	prepareCtx, cancel := context.WithTimeout(ctx, lifecycleTimeout)
	defer cancel()
	cmd := exec.CommandContext(prepareCtx, tool.Command, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	prepareErr := cmd.Run()
	if prepareErr != nil {
		return nil, errors.Join(
			fmt.Errorf("prepare %s session: %w: %s%s", policy, prepareErr, stderr.String(), stdout.String()),
			cleanupPreparedSession(dir),
		)
	}
	canonicalDir, err := canonicalPath(dir)
	if err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	session, err = readPreparedSession(canonicalDir)
	if err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	// Validation happens before the caller registers cleanup, so every malformed
	// descriptor produced by a successful prepare is closed here.
	if err = validatePreparedSession(session, tool, source); err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	return session, nil
}

func readPreparedSession(dir string) (*preparedSession, error) {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	var session preparedSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("session descriptor: %w", err)
	}
	session.Dir = dir
	session.RawJSON = append([]byte(nil), data...)
	session.Mock = session.Routes["mock"]
	return &session, nil
}

func validatePreparedSession(session *preparedSession, tool toolIdentity, source sourceSelection) error {
	if session.SchemaVersion != preparedSessionSchema {
		return fmt.Errorf("unsupported session schemaVersion %d", session.SchemaVersion)
	}
	if session.SessionID == "" || session.AgentType == "" || session.ToolSHA256 == "" ||
		session.Tool.Command == "" || len(session.Tool.Args) != 0 {
		return errors.New("prepared session identity is incomplete")
	}
	if source.Provider != "" && session.AgentType != source.Provider {
		return fmt.Errorf("prepared agent %s differs from requested %s", session.AgentType, source.Provider)
	}
	if session.AgentType != "codex" && session.AgentType != "claude" {
		return fmt.Errorf("unsupported prepared agent %q", session.AgentType)
	}
	if err := validateSource(session, source); err != nil {
		return err
	}
	toolPath, err := canonicalPath(session.Tool.Command)
	if err != nil {
		return fmt.Errorf("session tool: %w", err)
	}
	wantPath, err := canonicalPath(tool.Command)
	if err != nil {
		return fmt.Errorf("configured tool: %w", err)
	}
	if toolPath != wantPath {
		return errors.New("session tool differs from AGENT_SDK_TESTIFY_BIN")
	}
	actualToolHash, err := digestPath(toolPath)
	if err != nil {
		return err
	}
	if session.ToolSHA256 != actualToolHash || tool.SHA256 != actualToolHash {
		return errors.New("session tool hash differs from the executable")
	}
	if session.ToolVersion != "" && !strings.HasSuffix(tool.Version, session.ToolVersion) {
		return errors.New("session tool version differs from the executable")
	}
	for name, path := range map[string]string{
		"resourceRoot": session.ResourceRoot,
		"workspace":    session.Workspace,
		"home":         session.Home,
		"config":       session.Config,
		"tmp":          session.Tmp,
		"protocolCwd":  session.ProtocolCwd,
	} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("prepared session %s must be absolute", name)
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return fmt.Errorf("prepared session %s is unavailable", name)
		}
	}
	sessionPath, err := canonicalPath(session.Dir)
	if err != nil {
		return err
	}
	if pathWithin(session.Dir, session.ResourceRoot) && !pathWithin(session.ResourceRoot, sessionPath) {
		return errors.New("prepared session resource root contains the session directory")
	}
	if filepath.Clean(session.ResourceRoot) != filepath.Join(sessionPath, "runtime") {
		return errors.New("prepared session runtime must be session/runtime")
	}
	if err := validateRoute(session, toolPath); err != nil {
		return err
	}
	return nil
}

func validateSource(session *preparedSession, requested sourceSelection) error {
	source := session.Source
	if source.SHA256 == "" || !filepath.IsAbs(source.Path) {
		return errors.New("prepared source identity is incomplete")
	}
	sessionPath, err := canonicalPath(session.Dir)
	if err != nil {
		return err
	}
	sourcePath, err := canonicalPath(source.Path)
	if err != nil {
		return fmt.Errorf("prepared source: %w", err)
	}
	if !pathWithin(sourcePath, sessionPath) {
		return errors.New("prepared source is outside the session")
	}
	switch {
	case requested.Recording != "":
		if source.Kind != "recording" || source.Provenance != "external" {
			return errors.New("recording prepare did not produce an external recording source")
		}
		if !sourceDirectoryDigestMatches(sourcePath, source.SHA256) {
			return errors.New("prepared recording source changed after prepare")
		}
	case requested.Case != "":
		if source.Kind != "case" || source.ID != requested.Case || source.Provenance != "official" {
			return errors.New("prepared builtin case identity differs from --case")
		}
		hash, err := digestPath(sourcePath)
		if err != nil || hash != source.SHA256 {
			return errors.New("prepared case source changed after prepare")
		}
	default:
		return errors.New("prepare source is incomplete")
	}
	return nil
}

func sourceDirectoryDigestMatches(dir, want string) bool {
	planHash, planErr := digestPath(filepath.Join(dir, "plan.json"))
	manifestHash, manifestErr := digestPath(filepath.Join(dir, "manifest.json"))
	return planErr == nil && manifestErr == nil &&
		digest([]byte(planHash+"\n"+manifestHash+"\n")) == want
}

func validateRoute(session *preparedSession, toolPath string) error {
	route, ok := session.Routes["mock"]
	if !ok {
		return errors.New("prepared session has no mock route")
	}
	if !route.Enabled || route.Executor != "external-sdk" || route.Target != "replay" {
		return errors.New("session mock route is disabled or has the wrong executor")
	}
	switch session.Source.Kind {
	case "case":
		if route.Provenance != "official" {
			return errors.New("builtin case mock route is not official")
		}
	case "recording":
		if route.Provenance != "external" {
			return errors.New("recording session mock route has the wrong provenance")
		}
	default:
		return fmt.Errorf("unsupported prepared source kind %q", session.Source.Kind)
	}
	if !filepath.IsAbs(route.Workspace) || !filepath.IsAbs(route.ProtocolCwd) {
		return errors.New("mock route workspace and protocol cwd must be absolute")
	}
	if info, err := os.Stat(route.Workspace); err != nil || !info.IsDir() {
		return errors.New("mock route workspace is unavailable")
	}
	if info, err := os.Stat(route.ProtocolCwd); err != nil || !info.IsDir() {
		return errors.New("mock route protocol cwd is unavailable")
	}
	if len(route.Env) == 0 {
		return errors.New("mock route environment is empty")
	}
	for key, value := range route.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return errors.New("mock route environment is invalid")
		}
	}
	seen := map[string]bool{}
	for _, connection := range route.Connections {
		if connection.ID != "main" && connection.ID != "probe" {
			return fmt.Errorf("unsupported mock connection %q", connection.ID)
		}
		if seen[connection.ID] {
			return fmt.Errorf("duplicate mock connection %q", connection.ID)
		}
		seen[connection.ID] = true
		if err := validateToolCommand(command{Command: connection.Command, Args: connection.Args}, toolPath, session.ToolSHA256); err != nil {
			return fmt.Errorf("mock connection %s: %w", connection.ID, err)
		}
		if err := validateMockArgs(connection.Args, session.Dir, connection.ID); err != nil {
			return fmt.Errorf("mock connection %s: %w", connection.ID, err)
		}
		if connection.Launcher != "" {
			if !filepath.IsAbs(connection.Launcher) {
				return fmt.Errorf("mock connection %s launcher must be absolute", connection.ID)
			}
			info, err := os.Lstat(connection.Launcher)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("mock connection %s launcher is unavailable", connection.ID)
			}
		}
	}
	if !seen["main"] {
		return errors.New("prepared mock route has no main connection")
	}
	if session.AgentType == "claude" {
		main, _ := session.Connection("main")
		if main.Launcher == "" {
			return errors.New("claude mock main connection has no launcher")
		}
	}
	return nil
}

func validateToolCommand(value command, canonical, wantHash string) error {
	path, err := canonicalPath(value.Command)
	if err != nil {
		return err
	}
	if path != canonical {
		return errors.New("command differs from the prepared tool")
	}
	hash, err := digestPath(path)
	if err != nil {
		return err
	}
	if hash != wantHash {
		return errors.New("command hash differs from the prepared tool")
	}
	return nil
}

func validateMockArgs(args []string, sessionDir, connectionID string) error {
	if len(args) != 6 || args[0] != "agent" || args[1] != "mock" ||
		args[2] != "--session-dir" || args[3] != sessionDir ||
		args[4] != "--connection" || args[5] != connectionID {
		return errors.New("declared mock arguments differ from the prepared session")
	}
	return nil
}

func (s *preparedSession) Connection(id string) (preparedConnection, bool) {
	for _, connection := range s.Mock.Connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return preparedConnection{}, false
}

func (s *preparedSession) ConnectionIDs() []string {
	ids := make([]string, 0, len(s.Mock.Connections))
	for _, connection := range s.Mock.Connections {
		ids = append(ids, connection.ID)
	}
	sort.Strings(ids)
	return ids
}

func (s *preparedSession) HostEnv() []string {
	return hostEnvironment(s.Mock.Env)
}

func exportPreparedSession(ctx context.Context, tool toolIdentity, session *preparedSession, output string) error {
	if !filepath.IsAbs(output) {
		return errors.New("export output must be absolute")
	}
	if pathsOverlap(output, session.Dir) {
		return errors.New("export output must be outside the prepared session")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		if err == nil {
			return errors.New("export output already exists")
		}
		return err
	}
	exportCtx, cancel := context.WithTimeout(ctx, lifecycleTimeout)
	defer cancel()
	cmd := exec.CommandContext(exportCtx, tool.Command, "export", "--session-dir", session.Dir, "--output", output)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("export prepared material: %w: %s%s", err, stderr.String(), stdout.String())
	}
	return nil
}

func cleanupPreparedSession(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove prepared session: %w", err)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	return filepath.EvalSymlinks(path)
}

func digestPath(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathsOverlap(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}

func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.Clean(path) == path &&
		path != "." && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
