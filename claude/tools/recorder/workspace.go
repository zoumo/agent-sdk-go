// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace owns fresh synthetic project/config/storage. It never discovers or
// copies the user's default configuration, saved sessions or authentication.
type Workspace struct {
	Root, Project, Config, Home, Temp string
	owned                             string
	identity                          os.FileInfo
	configIdentity                    os.FileInfo
}

func PrepareWorkspace(path, scene string) (_ *Workspace, err error) {
	if _, err = Plan(scene); err != nil {
		return nil, err
	}
	if err = canonicalPath(path); err != nil {
		return nil, err
	}
	for p := path; ; p = filepath.Dir(p) {
		if info, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
			_, headErr := os.Lstat(filepath.Join(p, ".git", "HEAD"))
			if info.Mode().IsRegular() || headErr == nil {
				return nil, fmt.Errorf("private capture must be outside Git")
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err = os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	w := &Workspace{Root: path, Project: filepath.Join(path, "project"), Config: filepath.Join(path, "config"), Home: filepath.Join(path, "home"), Temp: filepath.Join(path, "tmp"), owned: path}
	w.identity, err = os.Lstat(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, w.Cleanup())
		}
	}()
	for _, dir := range []string{w.Project, w.Config, w.Home, w.Temp} {
		if err = os.Mkdir(dir, 0700); err != nil {
			return nil, err
		}
	}
	w.configIdentity, err = os.Lstat(w.Config)
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(filepath.Join(w.Project, "numbers.txt"), []byte("7\n11\n"), 0600)
	if err != nil {
		return nil, err
	}
	return w, nil
}
func (w *Workspace) validate() error {
	if w == nil || w.Root != w.owned || w.Project != filepath.Join(w.owned, "project") || w.Config != filepath.Join(w.owned, "config") || w.Home != filepath.Join(w.owned, "home") || w.Temp != filepath.Join(w.owned, "tmp") {
		return fmt.Errorf("owned synthetic workspace required")
	}
	for _, p := range []string{w.Root, w.Project, w.Config, w.Home, w.Temp} {
		if err := canonicalPath(p); err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			return fmt.Errorf("private workspace changed")
		}
	}
	info, _ := os.Lstat(w.Root)
	if !os.SameFile(w.identity, info) {
		return fmt.Errorf("workspace replaced")
	}
	return nil
}

// Environment passes recognized operator-supplied authentication opaquely. Its
// values are never inspected/logged; this does not certify usable authentication.
func (w *Workspace) Environment() []string {
	env := []string{"HOME=" + w.Home, "CLAUDE_CONFIG_DIR=" + w.Config, "TMPDIR=" + w.Temp, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1", "CLAUDE_CODE_SAFE_MODE=1"}
	for _, k := range []string{"PATH", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Cleanup removes only the directory identity created by PrepareWorkspace. Raw
// capture retention/publication is a separate operator decision.
func (w *Workspace) Cleanup() error {
	if w == nil || w.owned == "" {
		return fmt.Errorf("no workspace ownership")
	}
	info, err := os.Lstat(w.owned)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(w.identity, info) || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing cleanup of replaced workspace")
	}
	return os.RemoveAll(w.owned)
}

// Arguments is a per-version fixed launch plan, not proof that managed policy
// honors the requested flags. Preflight must pass before any CLI invocation.
func Arguments(scene string, w *Workspace) ([]string, error) {
	p, err := Plan(scene)
	if err != nil {
		return nil, err
	}
	if err = w.validate(); err != nil {
		return nil, err
	}
	tools, ask := "", ""
	switch scene {
	case "s1":
		tools, ask = "Read,Write", `"Read","Write"`
	case "s2":
		tools, ask = "AskUserQuestion", `"AskUserQuestion"`
	case "r2":
		tools, ask = "Read,Write,AskUserQuestion", `"Read","Write","AskUserQuestion"`
	}
	settings := `{"disableAllHooks":true,"permissions":{"ask":[` + ask + `]}}`
	args := []string{"--print", "--restricted", "--safe-mode", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-chrome", "--setting-sources", "", "--settings", settings, "--tools", tools, "--max-budget-usd", "1", "--system-prompt", "You perform only the provided synthetic fixture task. No external context, extra tools, or extra turns. Be concise."}
	if !p.Persist {
		args = append(args, "--no-session-persistence")
	}
	return args, nil
}
