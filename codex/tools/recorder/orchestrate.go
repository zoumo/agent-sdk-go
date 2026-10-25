package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	codex "github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tools/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

const s1 = "Synthetic fixture task. Use fixture_read exactly once on numbers.txt. Add the two integers. Use fixture_write exactly once on summary.txt with text exactly sum=42 followed by a newline. Then report the sum briefly. Do not use other tools or spawn agents."
const s2 = "Synthetic interaction fixture. Use request_user_input exactly once to ask which color to use: Amber or Indigo. Give exactly those two choices. After receiving the answer, report the chosen color briefly and finish. Do not use other tools or spawn agents."

// PeerOpener is the explicit capture transport seam. Tests supply only synthetic
// peers; it is never a CLI/account discovery or live fallback. finish must close
// and reap the owned transport, including when initialization fails.
type PeerOpener func(context.Context, LaunchProfile) (*codex.Client, func() (recording.TransportResult, error), error)

type CaptureResult struct {
	Error            string                     `json:"error,omitempty"`
	Scene            string                     `json:"scene"`
	Root             recording.RootResult       `json:"root"`
	Start            *types.ThreadStartResponse `json:"start,omitempty"`
	Original         recording.Result           `json:"original"`
	Query            recording.QueryEvidence    `json:"query"`
	Transport        recording.TransportResult  `json:"transport"`
	PromptSHA256     string                     `json:"promptSHA256,omitempty"`
	SDKRevision      string                     `json:"sdkRevision"`
	CLISHA256        string                     `json:"cliSHA256"`
	ProviderRequests *uint64                    `json:"providerRequests"`
	ProviderRetries  *uint64                    `json:"providerRetries"`
	USD              *float64                   `json:"usd"`
}

// CaptureWithPeer orchestrates an explicitly supplied peer after durable budget
// reservation. It is an importable offline test seam, not live authorization.
// Run keeps its independent financial/profile/isolation gates before selecting
// the native opener. No retry or alternate opener is attempted on any error.
func CaptureWithPeer(ctx context.Context, p LaunchProfile, open PeerOpener) (out CaptureResult, ret error) {
	out.Scene = p.Scene
	out.SDKRevision = p.SDKRevision
	out.CLISHA256 = p.CLISHA256
	if open == nil {
		return out, errors.New("explicit peer opener required")
	}
	if !validScene(p.Scene) {
		return out, errors.New("unsupported scene; children disabled")
	}
	if err := validateSceneConfig(p); err != nil {
		return out, err
	}
	deadline, err := ledgerDeadline(p.Ledger, p.SDKRevision)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	seconds := 120
	if p.Scene == "r4" {
		seconds = 30
	}
	ctx, stop := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer stop()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	for _, dir := range []string{p.Project, p.RawDir} {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return out, e
		}
		if len(entries) != 0 {
			return out, errors.New("fresh empty synthetic project/raw directories required")
		}
	}
	op := "root"
	if p.Scene == "r4" {
		op = "reader"
	}
	if err = reserveRevision(p.Ledger, p.SDKRevision, p.Scene, op, p.RawDir); err != nil {
		return out, err
	}
	defer func() {
		if ret != nil {
			ret = errors.Join(ret, reserveRevision(p.Ledger, p.SDKRevision, p.Scene, "stop", p.RawDir))
		}
	}()
	// Even an opener/initialize failure consumes the reservation.
	c, finish, err := open(ctx, p)
	if finish != nil {
		defer func() {
			transport, e := finish()
			out.Transport = transport
			ret = errors.Join(ret, e)
			if !transport.CleanupComplete || !transport.Reaped {
				ret = errors.Join(ret, errors.New("owned capture cleanup incomplete"))
			}
			if ret != nil {
				out.Error = ret.Error()
			}
			if transport.ProtocolBytes > recording.ProtocolLimit || transport.StderrBytes > recording.StderrLimit {
				ret = errors.Join(ret, errors.New("capture byte ceiling exceeded"))
			}
			if e = writeMetadata(p.RawDir, out); e != nil {
				ret = errors.Join(ret, e)
			}
		}()
	}
	if err != nil {
		return out, err
	}
	if c == nil || finish == nil {
		return out, errors.New("peer opener missing client or cleanup")
	}
	if p.Scene == "r4" {
		out.Query, err = recording.QuerySavedRoot(ctx, c, p.Project, p.SavedRootID)
		return out, err
	}
	thread := types.ThreadStartParams{Cwd: types.Some(p.Project), Ephemeral: types.Some(p.Scene != "r1"), Environments: types.Some([]types.TurnEnvironmentParams{}), Sandbox: types.Some(types.SandboxModeReadOnly), RuntimeWorkspaceRoots: types.Some([]types.AbsolutePathBuf{p.Project})}
	if p.Model != "" {
		thread.Model = types.Some(p.Model)
	}

	if p.Scene == "r2" {
		if err = writeExclusive(p.Project+"/numbers.txt", []byte("7\n11\n")); err != nil {
			return out, err
		}
	}
	if p.Scene == "s1" {
		if err = writeExclusive(p.Project+"/numbers.txt", []byte("17\n25\n")); err != nil {
			return out, err
		}
		var tools []types.DynamicToolSpec
		if err = json.Unmarshal([]byte(`[{"type":"function","name":"fixture_read","description":"Read the synthetic numbers.txt file only. One call allowed.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["numbers.txt"]}},"required":["path"],"additionalProperties":false}},{"type":"function","name":"fixture_write","description":"Write synthetic summary.txt once, only sum=42 followed by a newline.","inputSchema":{"type":"object","properties":{"path":{"type":"string","enum":["summary.txt"]},"text":{"type":"string","enum":["sum=42\n"]}},"required":["path","text"],"additionalProperties":false}}]`), &tools); err != nil {
			return out, err
		}
		thread.DynamicTools = types.Some(tools)
	}
	s, err := codex.Open(ctx, codex.SessionOptions{Client: c, Thread: thread})
	if err != nil {
		return out, err
	}
	defer func() { ret = errors.Join(ret, c.Close(), s.Close()) }()
	if start, ok := s.StartResponse(); ok {
		out.Start = &start
	}
	count := 0
	reserveTurn := func() error {
		count++
		return reserveRevision(p.Ledger, p.SDKRevision, p.Scene, "turn"+strconv.Itoa(count), p.RawDir)
	}
	h := &recording.Handler{Root: p.Project, Story: p.Scene}
	if p.Scene == "s1" || p.Scene == "s2" {
		prompt := s1
		if p.Scene == "s2" {
			prompt = s2
		}
		hash := sha256.Sum256([]byte(prompt))
		out.PromptSHA256 = hex.EncodeToString(hash[:])
		h.BeforeSubmit = reserveTurn
		// Preserve original text_elements=[] native payload for S1/S2.
		input := []types.UserInput{{Text: &types.TextUserInput{Type: "text", Text: prompt, TextElements: types.Some([]types.TextElement{})}}}
		out.Original, err = recording.RunSession(ctx, s, types.TurnStartParams{Input: input, Environments: types.Some([]types.TurnEnvironmentParams{})}, h)
		if err == nil && out.Original.Terminal.Status != types.TurnStatusCompleted {
			err = errors.New("native task did not complete successfully")
		}
		return out, err
	}
	h.Exact = p.NativePolicy
	out.Root, err = recording.RunRoot(ctx, s, recording.RootPlan{Scene: p.Scene, Model: p.Model, Effort: types.ReasoningEffort(p.Effort), Reserve: reserveTurn}, h)
	return out, err
}
func writeExclusive(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Sync(), f.Close())
}
func writeMetadata(dir string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeExclusive(dir+"/metadata.private.json", append(b, '\n'))
}

// Native CLI flags are fixed reviewed nonsecret overrides, with no user config
// discovery. Authentication transport/isolation remain separately blocking.
func nativeArgs(p LaunchProfile) []string {
	args := []string{"app-server", "--stdio"}
	settings := []string{`features.plugins=false`, `features.apps=false`, `features.enable_mcp_apps=false`, `features.recommended_plugins=false`, `features.skip_host_skill_discovery=true`, `skills.include_instructions=false`, `skills.bundled.enabled=false`, `features.memories=false`, `features.goals=false`, `notify=[]`, `features.shell_snapshot=false`, `project_doc_max_bytes=0`, `features.shell_tool=false`, `features.stable_environment_tools=false`, `features.multi_agent_v2=false`, `agents.enabled=false`, `features.code_mode=false`, `features.js_repl=false`, `web_search="disabled"`, `approvals_reviewer="user"`, `approval_policy="on-request"`, `analytics.enabled=false`, `otel.exporter="none"`, `otel.trace_exporter="none"`, `otel.metrics_exporter="none"`, `otel.log_user_prompt=false`, `otel.log_agent_responses=false`, `features.default_mode_request_user_input=true`, `features.request_permissions_tool=false`, `tools.update_plan.enabled=false`}
	for _, s := range settings {
		args = append(args, "-c", s)
	}
	return args
}
func proxyProfile(p LaunchProfile) error {
	op := "root"
	if p.Scene == "r4" {
		op = "reader"
	}
	if err := operateLedger(p.Ledger, p.SDKRevision, p.Scene, op, p.RawDir, true); err != nil {
		return err
	}
	if err := writeExclusive(p.RawDir+"/proxy.started", []byte("reserved launch; no retry\n")); err != nil {
		return err
	}

	seconds := 120
	if p.Scene == "r4" {
		seconds = 30
	}
	deadline, err := ledgerDeadline(p.Ledger, p.SDKRevision)
	if err != nil {
		return err
	}
	remaining := int(time.Until(deadline).Seconds())
	if remaining < seconds {
		seconds = remaining
	}
	if seconds <= 0 {
		return errors.New("aggregate deadline exhausted")
	}
	_, err = recording.Proxy(recording.ProxyConfig{Executable: p.CLI, Args: nativeArgs(p), Dir: p.Project, RawDir: p.RawDir, Seconds: seconds, Env: []string{"PATH=/usr/bin:/bin", "HOME=" + p.IsolatedHome, "CODEX_HOME=" + p.IsolatedHome}}, os.Stdin, os.Stdout)
	return err
}
func nativeOpener(profilePath string) PeerOpener {
	return func(ctx context.Context, p LaunchProfile) (*codex.Client, func() (recording.TransportResult, error), error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, nil, err
		}
		c, err := codex.NewStdio(ctx, codex.StdioOptions{Executable: exe, Args: []string{"--private-proxy", profilePath}, Dir: p.Project, Env: []string{"SDK_REAL_AGENT_E2E=1", "PATH=/usr/bin:/bin"}, Initialize: types.InitializeParams{ClientInfo: types.ClientInfo{Name: "bounded-recording", Version: "1"}, Capabilities: types.Some(types.InitializeCapabilities{ExperimentalAPI: types.Some(true)})}, Limits: codex.Limits{EventQueue: 2048, CallTimeout: 120 * time.Second, RequestTimeout: 15 * time.Second}})
		finish := func() (recording.TransportResult, error) {
			var out recording.TransportResult
			var closeErr error
			if c != nil {
				closeErr = c.Close()
			}
			b, e := os.ReadFile(p.RawDir + "/transport.json")
			if e == nil {
				e = json.Unmarshal(b, &out)
			}
			return out, errors.Join(closeErr, e)
		}
		return c, finish, err
	}
}
