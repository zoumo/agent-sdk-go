package agentpi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	_ "embed"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

const (
	sessionExtensionReady = "agent-sdk-session-ready"
	sessionInjectCommand  = "agent-sdk-inject"
)

//go:embed extensions/session.ts
var sessionExtensionSource []byte

type sessionExtensionConfig struct {
	ShellEnv             map[string]string  `json:"shellEnv,omitempty"`
	MCPServers           []sessionMCPServer `json:"mcpServers,omitempty"`
	Inject               bool               `json:"inject,omitempty"`
	ToolApproval         bool               `json:"toolApproval,omitempty"`
	AgentQuestions       bool               `json:"agentQuestions,omitempty"`
	InteractionTimeoutMS int64              `json:"interactionTimeoutMs,omitempty"`
}

type sessionExtension struct {
	path           string
	shellEnv       bool
	toolApproval   bool
	agentQuestions bool
	inject         bool
}

func materializeSessionExtension(config sessionExtensionConfig) (*sessionExtension, error) {
	if len(config.ShellEnv) == 0 && len(config.MCPServers) == 0 && !config.Inject && !config.ToolApproval && !config.AgentQuestions {
		return nil, nil
	}
	input, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "agent-sdk-pi-*")
	if err != nil {
		return nil, err
	}
	extension := &sessionExtension{
		path: filepath.Join(dir, "session.ts"), shellEnv: len(config.ShellEnv) != 0,
		toolApproval: config.ToolApproval, agentQuestions: config.AgentQuestions, inject: config.Inject,
	}
	if err := os.WriteFile(extension.path, sessionExtensionSource, 0o600); err != nil {
		return nil, errors.Join(err, extension.remove())
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), input, 0o600); err != nil {
		return nil, errors.Join(err, extension.remove())
	}
	return extension, nil
}

func (e *sessionExtension) check(ctx context.Context, native *pi.Session) error {
	if e == nil {
		return nil
	}
	commands, err := native.Commands(ctx)
	if err != nil {
		return err
	}
	ready, inject := false, !e.inject
	for _, command := range commands {
		if command.Source != "extension" || (command.Name != sessionExtensionReady && command.Name != sessionInjectCommand) {
			continue
		}
		var info struct{ Path string }
		if err := json.Unmarshal(command.SourceInfo, &info); err != nil {
			return err
		}
		if !sameExtensionFile(info.Path, e.path) {
			continue
		}
		switch command.Name {
		case sessionExtensionReady:
			ready = true
		case sessionInjectCommand:
			inject = true
		}
	}
	if !ready || !inject {
		return unsupported("pi_session_extension_unavailable", "Pi 1.6.0 SDK session extension did not initialize")
	}
	return nil
}

// The CLI can report the lexical path supplied by its launcher. Accept an
// alias only when it identifies the exact file materialized for this session.
func sameExtensionFile(reported, materialized string) bool {
	if reported == materialized {
		return true
	}
	if !filepath.IsAbs(reported) {
		return false
	}
	actual, err := os.Stat(reported)
	if err != nil {
		return false
	}
	expected, err := os.Stat(materialized)
	if err != nil || !os.SameFile(actual, expected) {
		return false
	}
	// The extension reads its launch settings relative to its source path.
	actualConfig, err := os.Stat(filepath.Join(filepath.Dir(reported), "session.json"))
	if err != nil {
		return false
	}
	expectedConfig, err := os.Stat(filepath.Join(filepath.Dir(materialized), "session.json"))
	return err == nil && os.SameFile(actualConfig, expectedConfig)
}

func (e *sessionExtension) remove() error {
	if e == nil {
		return nil
	}
	return os.RemoveAll(filepath.Dir(e.path))
}
