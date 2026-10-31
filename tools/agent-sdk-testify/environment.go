package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func prepareNative(ctx context.Context, o *options, plan object, root string) error {
	if o.CLI == "" {
		o.CLI = str(plan["cli"])
	}
	if o.CLI == "" {
		o.CLI = str(plan["provider"])
	}
	cli, err := absoluteExecutable(o.CLI)
	if err != nil {
		return err
	}
	plan["cli"] = cli
	delete(plan, "cliVersion")
	if data, e := os.ReadFile(cli); e == nil {
		sum := sha256.Sum256(data)
		plan["cliSHA256"] = hex.EncodeToString(sum[:])
	} else {
		return e
	}
	if o.Model != "" {
		plan["model"] = o.Model
	}
	var version bytes.Buffer
	probe := exec.Command(cli, "--version")
	probe.Stdout = &version
	probe.Dir = root
	if err := waitDriver(ctx, probe); ctx.Err() != nil {
		return ctx.Err()
	} else if err == nil {
		plan["cliVersion"] = strings.TrimSpace(version.String())
	}
	provider := str(plan["provider"])
	profile := str(plan["driverProfile"])
	params := obj(obj(plan["scenario"])["parameters"])
	args := stringsOf(plan["args"])
	if len(args) == 0 {
		if provider == "codex" {
			args = []string{"app-server", "--stdio", "-c", "features.default_mode_request_user_input=true"}
			if profile == "children" || profile == "background-children" || params["historyTarget"] == "child-messages" || params["topology"] != nil {
				args = append(args, "-c", "agents.enabled=true", "-c", "features.multi_agent=true")
			}
			if params["topology"] != nil {
				args = append(args, "-c", "agents.max_depth=2")
			}
			if params["approvalKind"] == "command" {
				args = append(args, "-c", `sandbox_mode="read-only"`)
			}
		} else {
			args = []string{"--print", "--system-prompt", "Complete the supplied synthetic task in the working directory. Be concise.", "--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
			if profile != "history" && profile != "existing-children" {
				args = append(args, "--no-session-persistence")
			}
			if profile == "children" || profile == "background-children" || params["topology"] != nil {
				args = append(args, "--forward-subagent-text")
			}
			var ask []string
			switch profile {
			case "fixture-tools":
				ask = []string{"Read", "Write"}
			case "deferred-approval":
				ask = []string{"Read"}
			case "children", "background-children":
				ask = []string{"Agent", "Read"}
			}
			if params["approvalKind"] == "command" {
				ask = []string{"Bash"}
			}
			if ask != nil {
				b, _ := json.Marshal(object{"permissions": object{"ask": ask}})
				args = append(args, "--settings", string(b))
			}
			if params["applicationTool"] == true {
				args = append(args, "--mcp-config", `{"mcpServers":{"fixture":{"type":"sdk","name":"fixture"}}}`)
			}
			if params["configurationTiming"] == "creation" && params["setting"] == "thinking" {
				args = append(args, "--max-thinking-tokens", "1024")
			}
			if params["setting"] == "effort" {
				args = append(args, "--effort", "low")
			}
		}
	}
	if provider == "claude" && str(plan["model"]) != "" {
		args = append(args, "--model", str(plan["model"]))
	}
	plan["args"] = args
	if provider == "claude" && profile == "existing-children" {
		args = append(args, "--resume", str(obj(plan["historySeed"])["sessionId"]), "--forward-subagent-text")
		plan["args"] = args
	}
	plan["stdinEOFAction"] = "close"
	if provider == "claude" && profile == "interrupt" {
		plan["stdinEOFAction"] = "interrupt"
	}
	if profile == "children" || profile == "background-children" {
		if err = writeFiles(str(plan["project"]), object{"child-fixture.txt": "SYNTHETIC_CHILD_TOKEN\n"}); err != nil {
			return err
		}
		if provider == "codex" {
			prompt := "Use fixture_read exactly once to read only child-fixture.txt in the inherited working directory. Return exactly SYNTHETIC_CHILD_TOKEN. Do not create agents or descendants. Do not use shell, network, search, writes, or any other file or tool."
			b, _ := json.Marshal(prompt)
			role := "name=\"fixture_reader\"\ndescription=\"Read the single synthetic child fixture with fixture_read.\"\ndeveloper_instructions=" + string(b) + "\n"
			if err = writeFiles(filepath.Join(root, "config"), object{"agents/fixture_reader.toml": role}); err != nil {
				return err
			}
		}
	}
	env := map[string]string{}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if v, ok := os.LookupEnv(key); ok {
			env[key] = v
		}
	}
	if o.Config != "" {
		files := []string{"config.toml", "auth.json"}
		if provider == "claude" {
			files = []string{"settings.json", ".credentials.json"}
		}
		for _, name := range files {
			source := filepath.Join(o.Config, name)
			if _, e := os.Stat(source); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return e
			}
			if err = copyFile(source, filepath.Join(root, "config", name)); err != nil {
				return err
			}
		}
		if provider == "claude" {
			var settings object
			if e := readJSON(filepath.Join(root, "config/settings.json"), &settings); e == nil {
				selected := obj(settings["env"])
				// A selected endpoint must keep its own credentials; inherited
				// auth from a different endpoint is not a substitute.
				if str(selected["ANTHROPIC_BASE_URL"]) != "" {
					for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
						delete(env, k)
					}
				}
				for k, v := range selected {
					if s, ok := v.(string); ok {
						env[k] = s
					}
				}
			}
		} else {
			if err = codexAuthEnvironment(filepath.Join(root, "config/config.toml"), env); err != nil {
				return err
			}
		}
	}
	// No arbitrary source HOME, dependency installation or SDK version probes.
	return writeJSON(filepath.Join(root, "native.env.private.json"), env)
}

// Select only variables referenced by the active native provider. A real TOML
// parser handles table and inline-table forms without a new config language.
func codexAuthEnvironment(path string, env map[string]string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var config struct {
		Provider  string `toml:"model_provider"`
		Providers map[string]struct {
			EnvKey  string            `toml:"env_key"`
			Headers map[string]string `toml:"env_http_headers"`
		} `toml:"model_providers"`
	}
	if err = toml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("selected Codex config: %w", err)
	}
	selected := config.Providers[config.Provider]
	names := []string{selected.EnvKey}
	for _, name := range selected.Headers {
		names = append(names, name)
	}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); name != "" && ok {
			env[name] = value
		}
	}
	return nil
}

func scrubNative(root string) error {
	var errs []error
	for _, name := range []string{"native.env.private.json", "config/auth.json", "config/.credentials.json", "config/config.toml", "config/settings.json"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
