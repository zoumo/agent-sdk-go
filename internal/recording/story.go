// Package recording contains tool-only private capture and replay primitives.
// Native SDK runtime packages must not depend on this package.
package recording

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

type StoryPlan struct {
	Version      int      `json:"version"`
	Provider     string   `json:"provider"`
	Story        string   `json:"story"`
	CLI          string   `json:"cli"`
	CLIVersion   string   `json:"cliVersion"`
	CLISHA256    string   `json:"cliSHA256"`
	DriverSHA256 string   `json:"driverSHA256"`
	SDKRevision  string   `json:"sdkRevision"`
	SourceSHA256 string   `json:"sourceSHA256"`
	Root         string   `json:"root"`
	Project      string   `json:"project"`
	Home         string   `json:"home"`
	Config       string   `json:"config"`
	Raw          string   `json:"raw"`
	Model        string   `json:"model"`
	Args         []string `json:"args"`
	Prompts      []string `json:"prompts"`
}

type StoryReport struct {
	Provider string   `json:"provider"`
	Story    string   `json:"story"`
	Status   string   `json:"status"`
	Inputs   int      `json:"inputs"`
	Results  int      `json:"results"`
	Events   int      `json:"events"`
	Requests int      `json:"requests"`
	Facts    []string `json:"facts"`
	Gaps     []string `json:"gaps"`
	// Diagnostics and native control/query receipts stay in the private directory.
	Receipts []any `json:"receipts,omitempty"`
}

var storyID = regexp.MustCompile(`^SDK-US-0(0[1-9]|1[0-9])$`)

func LoadStory(path, provider string, replay bool) (p StoryPlan, err error) {
	b, e := os.ReadFile(path)
	if e != nil || len(b) > 64<<10 {
		return p, errors.New("recording plan unavailable or too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return p, errors.New("invalid recording plan")
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return p, errors.New("trailing plan data")
	}
	if p.Version != 1 || p.Provider != provider || !storyID.MatchString(p.Story) || len(p.Prompts) > 2 {
		return p, errors.New("unsupported story plan")
	}
	for _, dir := range []string{p.Root, p.Home, p.Config, p.Project, p.Raw} {
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return p, errors.New("absolute private paths required")
		}
		st, e := os.Lstat(dir)
		if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
			return p, errors.New("private recording directory required")
		}
		if dir != p.Root && filepath.Dir(dir) != p.Root {
			return p, errors.New("story directories must be direct owned children")
		}
		for part := dir; part != "/"; part = filepath.Dir(part) {
			st, e := os.Lstat(part)
			if e != nil || st.Mode()&os.ModeSymlink != 0 {
				return p, errors.New("recording symlink refused")
			}
			if git, gitErr := os.Lstat(filepath.Join(part, ".git")); gitErr == nil && (git.Mode().IsRegular() || gitHeadExists(filepath.Join(part, ".git", "HEAD"))) {
				return p, errors.New("record outside Git worktrees")
			}
		}
	}
	if !replay {
		if e := validateStoryArguments(p); e != nil {
			return p, e
		}
		if os.Getenv("SDK_REAL_AGENT_E2E") != "1" || os.Getenv("CI") != "" {
			return p, errors.New("live opt-in required; CI recording forbidden")
		}
		if !filepath.IsAbs(p.CLI) || len(p.CLISHA256) != 64 || len(p.DriverSHA256) != 64 {
			return p, errors.New("exact native and driver identities required")
		}
		versions := ReviewedStoryVersions(provider)
		known := false
		for _, version := range versions {
			if version == p.CLIVersion {
				known = true
			}
		}
		if !known {
			return p, errors.New("unreviewed native recording version")
		}
		exe, e := os.Executable()
		if e != nil {
			return p, e
		}
		for _, entry := range [][2]string{{p.CLI, p.CLISHA256}, {exe, p.DriverSHA256}} {
			st, e := os.Lstat(entry[0])
			if e != nil || !st.Mode().IsRegular() || st.Size() > 512<<20 || st.Mode().Perm()&0111 == 0 {
				return p, errors.New("bounded regular recording executable required")
			}
			f, e := os.Open(entry[0])
			if e != nil {
				return p, errors.New("recording executable unavailable")
			}
			h := sha256.New()
			_, e = io.Copy(h, f)
			f.Close()
			if e != nil || hex.EncodeToString(h.Sum(nil)) != entry[1] {
				return p, errors.New("recording executable digest mismatch")
			}
		}
	}
	return p, nil
}

func ReserveStory(root string) error {
	f, e := os.OpenFile(filepath.Join(root, "attempt.started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("story already attempted or cannot reserve; no retry")
	}
	_, e = f.WriteString("reserved before native launch; never reset\n")
	return errors.Join(e, f.Sync(), f.Close())
}

func WritePrivate(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func NativeEnvironment(p StoryPlan) ([]string, error) {
	path := filepath.Join(p.Root, "native.env.private.json")
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 64<<10 {
		return nil, errors.New("private native environment required")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var values map[string]string
	if json.Unmarshal(b, &values) != nil {
		return nil, errors.New("invalid private environment")
	}
	if values["HOME"] != p.Home || values["TMPDIR"] != filepath.Join(p.Root, "tmp") {
		return nil, errors.New("independent HOME and temporary directory required")
	}
	configKey := "CODEX_HOME"
	if p.Provider == "claude" {
		configKey = "CLAUDE_CONFIG_DIR"
	}
	if values[configKey] != p.Config {
		return nil, errors.New("isolated native configuration required")
	}
	var env []string
	for k, v := range values {
		allowed := map[string]bool{"HOME": true, "TMPDIR": true, "PATH": true, "LANG": true, "DISABLE_AUTOUPDATER": true, "CODEX_HOME": true, "CLAUDE_CONFIG_DIR": true, "CLAUDE_CODE_DISABLE_AUTO_MEMORY": true, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "OPENAI_BASE_URL": true, "OPENAI_ORGANIZATION": true, "OPENAI_PROJECT": true, "ANTHROPIC_BASE_URL": true, "ANTHROPIC_MODEL": true, "ANTHROPIC_DEFAULT_SONNET_MODEL": true, "ANTHROPIC_DEFAULT_OPUS_MODEL": true, "ANTHROPIC_DEFAULT_HAIKU_MODEL": true}
		if !allowed[k] && !regexp.MustCompile(`^[A-Z][A-Z0-9_]*(KEY|TOKEN)$`).MatchString(k) {
			return nil, errors.New("unreviewed native environment key")
		}
		if k == "PATH" && v != "/usr/local/bin:/usr/bin:/bin" {
			return nil, errors.New("fixed native PATH required")
		}
		if strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, '\x00') {
			return nil, errors.New("invalid environment entry")
		}
		env = append(env, k+"="+v)
	}
	return env, nil
}

func ReadFrames(path string) ([]Frame, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if st, e := f.Stat(); e != nil || st.Size() > 16<<20 {
		return nil, errors.New("recording file byte limit")
	}
	s := bufio.NewScanner(io.LimitReader(f, 16<<20))
	s.Buffer(make([]byte, 4096), 3<<20)
	var frames []Frame
	for s.Scan() {
		var fr Frame
		if json.Unmarshal(s.Bytes(), &fr) != nil || fr.Ordinal != len(frames)+1 || (fr.Direction != "c2s" && fr.Direction != "s2c") || !json.Valid(fr.Wire) {
			return nil, errors.New("invalid recording frame")
		}
		frames = append(frames, fr)
		if len(frames) > 10000 {
			return nil, errors.New("recording frame limit")
		}
	}
	if e = s.Err(); e != nil {
		return nil, e
	}
	if len(frames) == 0 {
		return nil, errors.New("empty recording")
	}
	return frames, nil
}

// ReplayFrames never opens a CLI, credentials, account or network. It matches
// complete caller JSON, retains server wire bytes, and requires caller EOF.
func ReplayFrames(in io.Reader, out io.Writer, frames []Frame) error {
	r := bufio.NewReaderSize(in, 4096)
	for _, f := range frames {
		if f.Direction == "c2s" {
			got, e := readReplayLine(r)
			if e != nil || len(got) > ProtocolLimit {
				return errors.New("missing or oversized replay request")
			}
			var a, b any
			if decodeReplayJSON(got, &a) != nil || decodeReplayJSON(f.Wire, &b) != nil {
				return errors.New("invalid replay request")
			}
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if !bytes.Equal(aa, bb) {
				return errors.New("replay request mismatch")
			}
		} else if f.Direction == "s2c" {
			if _, e := out.Write(append(bytes.Clone(f.Wire), '\n')); e != nil {
				return e
			}
		} else {
			return errors.New("invalid replay direction")
		}
	}
	_, e := r.ReadByte()
	if e != io.EOF {
		return errors.New("extra replay request")
	}
	return nil
}

// StoryProxy is invoked only by the record command's owned SDK transport.
func StoryProxy(p StoryPlan, replay bool, extra []string) error {
	if replay {
		frames, e := ReadFrames(filepath.Join(p.Raw, "raw.jsonl"))
		if e != nil {
			return e
		}
		e = ReplayFrames(os.Stdin, os.Stdout, frames)
		if e == nil {
			e = WritePrivate(filepath.Join(p.Root, "replay-peer.json"), map[string]any{"frames": len(frames), "fullConsumption": true, "nativeLaunched": false})
		}
		return e
	}
	env, e := NativeEnvironment(p)
	if e != nil {
		return e
	}
	// A proxy invocation cannot repeat an already reserved native launch.
	f, e := os.OpenFile(filepath.Join(p.Root, "native.started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("native launch already attempted")
	}
	if e = f.Close(); e != nil {
		return e
	}
	args := append(append([]string{}, p.Args...), extra...)
	_, e = Proxy(ProxyConfig{Executable: p.CLI, Args: args, Env: env, Dir: p.Project, RawDir: p.Raw, Seconds: 120}, os.Stdin, storyOutput{p: p})
	return e
}

type storyOutput struct{ p StoryPlan }

func (w storyOutput) Write(b []byte) (int, error) {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return 0, errors.New("non-JSON native output")
	}
	sub, _ := m["subtype"].(string)
	method, _ := m["method"].(string)
	if strings.HasPrefix(sub, "hook_") || strings.HasPrefix(method, "hook/") || strings.HasPrefix(sub, "task_") {
		return 0, errors.New("unexpected hook or child output")
	}
	if parent, ok := m["parent_tool_use_id"].(string); ok && parent != "" {
		return 0, errors.New("child output outside root story")
	}
	return os.Stdout.Write(b)
}

func gitHeadExists(path string) bool {
	st, err := os.Stat(path)
	return (err == nil && st.Mode().IsRegular()) || (err != nil && !os.IsNotExist(err))
}

func decodeReplayJSON(b []byte, value *any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(value); err != nil {
		return err
	}
	var more any
	if d.Decode(&more) != io.EOF {
		return errors.New("trailing replay JSON")
	}
	return nil
}
func readReplayLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > ProtocolLimit {
			return nil, errors.New("replay request byte limit")
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

// StoryCommand handles only explicit story modes; legacy record interfaces keep
// their original gates. Provider-specific SDK operations stay in native tools.
func StoryCommand(provider string, peerArgs func(StoryPlan) []string, run func(context.Context, StoryPlan, bool, *StoryReport) error, replayPeer func(StoryPlan) error) (bool, error) {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[1], "--story-") {
		return false, nil
	}
	if os.Args[1] == "--story-template" {
		if len(os.Args) != 3 {
			return true, errors.New("one story ID required")
		}
		args, e := StoryArguments(provider, os.Args[2])
		if e == nil {
			e = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args, "cliVersions": ReviewedStoryVersions(provider)})
		}
		return true, e
	}
	if len(os.Args) < 3 {
		return true, errors.New("story plan required")
	}
	mode := os.Args[1]
	if mode == "--story-plan" || mode == "--story-proxy" {
		return true, errors.New("Go-driven live stories retired; use tools/recording official Python SDK module; historical replay remains available")
	}
	replay := mode == "--story-replay" || mode == "--story-peer"
	if mode != "--story-plan" && mode != "--story-replay" && mode != "--story-proxy" && mode != "--story-peer" {
		return true, errors.New("unknown story command")
	}
	p, e := LoadStory(os.Args[2], provider, replay)
	if e != nil {
		return true, e
	}
	if mode == "--story-proxy" || mode == "--story-peer" {
		var extra []string
		if peerArgs != nil {
			extra = peerArgs(p)
		}
		if len(os.Args[3:]) != len(extra) || (len(extra) > 0 && !reflect.DeepEqual(os.Args[3:], extra)) {
			return true, errors.New("unreviewed SDK transport arguments")
		}
		if replay && replayPeer != nil {
			e := replayPeer(p)
			if e == nil {
				e = WritePrivate(filepath.Join(p.Root, "replay-peer.json"), map[string]any{"fullConsumption": true, "nativeLaunched": false, "privateAnswerSettlement": true})
			}
			return true, e
		}
		return true, StoryProxy(p, replay, extra)
	}
	if len(os.Args) != 3 {
		return true, errors.New("unexpected story arguments")
	}
	if !replay {
		if e = ReserveStory(p.Root); e != nil {
			return true, e
		}
	}
	report := StoryReport{Provider: provider, Story: p.Story, Status: "partial"}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	e = run(ctx, p, replay, &report)
	if e != nil {
		report.Status = "failed"
		name := "diagnostics.private.json"
		if replay {
			name = "replay-diagnostics.private.json"
		}
		_ = WritePrivate(filepath.Join(p.Root, name), map[string]string{"error": e.Error()})
	}
	name := "report.json"
	if replay {
		name = "replay-report.json"
	}
	if w := WritePrivate(filepath.Join(p.Root, name), report); w != nil {
		return true, w
	}
	fmt.Printf("%s %s: %s\n", provider, p.Story, report.Status)
	if e != nil {
		return true, errors.New("story failed; private diagnostics retained")
	}
	return true, nil
}
