package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agenttestify "github.com/zoumo/agent-sdk-testify"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

const (
	preparedSessionSchema = 5
	lifecycleTimeout      = 30 * time.Second
)

type preparedConnection struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
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
	Tool          toolIdentity             `json:"tool"`
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

	Dir     string                `json:"-"`
	RawJSON []byte                `json:"-"`
	Mock    preparedRoute         `json:"-"`
	Fixture *agenttestify.Fixture `json:"-"`
	Runner  *mocktest.Session     `json:"-"`
}

type sourceSelection struct {
	Provider  string
	Case      string
	Recording string
}

func prepareSession(ctx context.Context, tool toolIdentity, source sourceSelection, dir, policy string) (session *preparedSession, err error) {
	if ctx == nil {
		return nil, errors.New("prepare requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("prepared session directory must be absolute")
	}
	if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
		if statErr == nil {
			return nil, fmt.Errorf("prepared session directory already exists: %s", dir)
		}
		return nil, statErr
	}
	options := agenttestify.PrepareOptions{
		FixtureDir:       dir,
		EmbeddedIdentity: mocktest.EmbeddedIdentity(),
	}
	switch {
	case source.Recording != "":
		if !filepath.IsAbs(source.Recording) {
			return nil, errors.New("--recording must be absolute")
		}
		options.Recording = source.Recording
	case source.Case != "":
		options.Agent = source.Provider
		options.Case = source.Case
	default:
		return nil, errors.New("prepare requires --case with --provider or --recording")
	}
	fixture, prepareErr := agenttestify.Prepare(options)
	if prepareErr != nil {
		return nil, errors.Join(fmt.Errorf("prepare %s session: %w", policy, prepareErr), cleanupPreparedSession(dir))
	}
	session, err = viewPreparedSession(fixture)
	if err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	session.Runner, err = mocktest.FromFixture(fixture)
	if err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	if err = validatePreparedSession(session, tool, source); err != nil {
		return nil, errors.Join(err, cleanupPreparedSession(dir))
	}
	return session, nil
}

func readPreparedSession(dir string) (*preparedSession, error) {
	canonicalDir, err := canonicalPath(dir)
	if err != nil {
		return nil, err
	}
	fixture, err := agenttestify.Open(canonicalDir)
	if err != nil {
		return nil, err
	}
	session, err := viewPreparedSession(fixture)
	if err != nil {
		return nil, err
	}
	session.Runner, err = mocktest.FromFixture(fixture)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func viewPreparedSession(fixture *agenttestify.Fixture) (*preparedSession, error) {
	route := fixture.Routes["mock"]
	connections := make([]preparedConnection, len(route.Connections))
	for i, connection := range route.Connections {
		connections[i] = preparedConnection{
			ID:        connection.ID,
			Transport: string(connection.Transport),
		}
	}
	session := &preparedSession{
		SchemaVersion: preparedSessionSchema,
		SessionID:     fixture.FixtureID,
		Tool: toolIdentity{
			Source:         string(fixture.Tool.Source),
			Version:        fixture.Tool.Version,
			Module:         fixture.Tool.Module,
			ModuleSum:      fixture.Tool.ModuleSum,
			ModuleRevision: fixture.Tool.ModuleRevision,
			ModuleTime:     fixture.Tool.ModuleTime,
			GoVersion:      fixture.Tool.GoVersion,
		},
		AgentType:    fixture.AgentType,
		ResourceRoot: fixture.ResourceRoot,
		Workspace:    fixture.Workspace,
		Home:         fixture.Home,
		Config:       fixture.Config,
		Tmp:          fixture.Tmp,
		ProtocolCwd:  fixture.ProtocolCwd,
		Env:          mapsClone(fixture.Env),
		Routes: map[string]preparedRoute{"mock": {
			Enabled:     route.Enabled,
			Executor:    route.Executor,
			Target:      route.Target,
			Provenance:  route.Provenance,
			Workspace:   route.Workspace,
			ProtocolCwd: route.ProtocolCwd,
			Env:         mapsClone(route.Env),
			Connections: connections,
		}},
		Source: preparedSource{
			Kind:          fixture.Source.Kind,
			ID:            fixture.Source.ID,
			Path:          fixture.Source.Path,
			SHA256:        fixture.Source.SHA256,
			Provenance:    fixture.Source.Provenance,
			DriverProfile: fixture.Source.DriverProfile,
		},
		Dir:     fixture.FixtureDir,
		Fixture: fixture,
	}
	session.Mock = session.Routes["mock"]
	rawJSON, err := json.Marshal(session)
	if err != nil {
		return nil, fmt.Errorf("marshal prepared session: %w", err)
	}
	session.RawJSON = rawJSON
	return session, nil
}

func mapsClone(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	maps.Copy(cloned, values)
	return cloned
}

func validatePreparedSession(session *preparedSession, tool toolIdentity, source sourceSelection) error {
	if session.SchemaVersion != preparedSessionSchema {
		return fmt.Errorf("unsupported session schemaVersion %d", session.SchemaVersion)
	}
	if session.SessionID == "" || session.AgentType == "" || session.Fixture == nil ||
		session.Tool.Source != "embedded" ||
		session.Tool.Module != "github.com/zoumo/agent-sdk-testify" ||
		session.Tool.Version == "" || session.Tool.ModuleSum == "" {
		return errors.New("prepared session identity is incomplete")
	}
	if tool.Source != session.Tool.Source || tool.Module != session.Tool.Module ||
		tool.ModuleSum != session.Tool.ModuleSum || tool.Version != session.Tool.Version ||
		(session.Tool.ModuleRevision != "" && tool.ModuleRevision != session.Tool.ModuleRevision) ||
		(session.Tool.ModuleTime != "" && tool.ModuleTime != session.Tool.ModuleTime) ||
		(session.Tool.GoVersion != "" && tool.GoVersion != session.Tool.GoVersion) {
		return errors.New("prepared session tool differs from the linked library")
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
	if err := validateRoute(session); err != nil {
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

func validateRoute(session *preparedSession) error {
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
		if connection.Transport != string(agenttestify.ConnectionTransportInProcess) {
			return fmt.Errorf("mock connection %s is not an embedded in-process route", connection.ID)
		}
	}
	if !seen["main"] {
		return errors.New("prepared mock route has no main connection")
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

func exportPreparedSession(ctx context.Context, session *preparedSession, output string) error {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := agenttestify.Export(agenttestify.ExportOptions{FixtureDir: session.Dir, Output: output}); err != nil {
		return fmt.Errorf("export prepared material: %w", err)
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
