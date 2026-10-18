// record-events is an opt-in maintenance tool. Default tests never invoke Codex.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/internal/recording"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

const s1 = "Synthetic fixture task. Use fixture_read exactly once on numbers.txt. Add the two integers. Use fixture_write exactly once on summary.txt with text exactly sum=42 followed by a newline. Then report the sum briefly. Do not use other tools or spawn agents."
const s2 = "Synthetic interaction fixture. Use request_user_input exactly once to ask which color to use: Amber or Indigo. Give exactly those two choices. After receiving the answer, report the chosen color briefly and finish. Do not use other tools or spawn agents."

func run() error {
	if len(os.Args) != 3 {
		return errors.New("usage: record-events --record|--proxy private-config.json")
	}
	mode := ""
	if os.Args[1] == "--record" || os.Args[1] == "--proxy" {
		mode = "record"
	}
	if err := recording.Guard(mode); err != nil {
		return err
	}
	b, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	var cfg recording.ProxyConfig
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	if cfg.Seconds != 120 || cfg.Executable == "" || cfg.Dir == "" || cfg.RawDir == "" {
		return errors.New("missing bounded configuration")
	}
	cfg.Env = runtimeEnvironment()
	if os.Args[1] == "--proxy" {
		_, err = recording.Proxy(cfg, os.Stdin, os.Stdout)
		return err
	}
	// An exclusive per-story marker counts even startup failure. No resume/retry.
	marker, err := os.OpenFile(cfg.RawDir+"/attempted", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("story already attempted")
	}
	marker.Close()
	story := os.Getenv("CODEX_RECORD_STORY")
	prompt := s1
	if story == "s2" {
		prompt = s2
	} else if story != "s1" {
		return errors.New("only s1/s2 supported; subagent scenario blocked")
	}
	if err = os.WriteFile(cfg.Dir+"/numbers.txt", []byte("17\n25\n"), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	init := types.InitializeParams{ClientInfo: types.ClientInfo{Name: "bounded-recording", Version: "1"}, Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})}
	start := time.Now()
	c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: executable, Args: []string{"--proxy", os.Args[2]}, Dir: cfg.Dir, Env: append(runtimeEnvironment(), "SDK_REAL_AGENT_E2E=1"), Initialize: init, Limits: codex.Limits{EventQueue: 2048, CallTimeout: 120 * time.Second, RequestTimeout: 15 * time.Second}})
	if err != nil {
		return err
	}
	sandbox := types.SandboxModeReadOnly
	thread := types.ThreadStartParams{Cwd: types.Some(cfg.Dir), Ephemeral: types.Some(true), Environments: types.Some([]types.TurnEnvironmentParams{}), Sandbox: types.Some(sandbox), RuntimeWorkspaceRoots: types.Some([]types.AbsolutePathBuf{types.AbsolutePathBuf(cfg.Dir)})}
	if story == "s1" {
		var tools []types.DynamicToolSpec
		if err = json.Unmarshal([]byte(`[{"type":"function","name":"fixture_read","description":"Read the synthetic numbers.txt file only. One call allowed.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["numbers.txt"]}},"required":["path"],"additionalProperties":false}},{"type":"function","name":"fixture_write","description":"Write synthetic summary.txt once, only sum=42 followed by a newline.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["summary.txt"]},"text":{"type":"string","enum":["sum=42\n"]}},"required":["path","text"],"additionalProperties":false}}]`), &tools); err != nil {
			return err
		}
		thread.DynamicTools = types.Some(tools)
	}
	var input []types.UserInput
	raw, _ := json.Marshal([]map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}})
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	turn := types.TurnStartParams{Input: input, Environments: types.Some([]types.TurnEnvironmentParams{})}
	result, sessionErr := recording.Session(ctx, c, thread, turn, &recording.Handler{Root: cfg.Dir, Story: story})
	closeErr := c.Close()
	h := sha256.Sum256([]byte(prompt))
	metadata := map[string]any{"root": cfg.Dir, "home": os.Getenv("CODEX_RECORD_HOME"), "promptSHA256": hex.EncodeToString(h[:]), "cliSHA256": os.Getenv("CODEX_RECORD_CLI_SHA256"), "sdkCommit": os.Getenv("CODEX_RECORD_SDK_SHA"), "milliseconds": time.Since(start).Milliseconds(), "completed": result.Completed, "tools": result.Tools, "questions": result.Questions, "observations": result.Observations, "sessionError": sessionErr != nil, "closeError": closeErr != nil}
	raw, _ = json.Marshal(metadata)
	if err = os.WriteFile(cfg.RawDir+"/metadata.private.json", raw, 0600); err != nil {
		return err
	}
	if sessionErr != nil {
		return errors.New("recorded session failed; inspect private metadata without publishing raw diagnostics")
	}
	return closeErr
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func runtimeEnvironment() []string {
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, key := range []string{"HOME", "CODEX_HOME", "TMPDIR", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}
