// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrDiscoveryCoverage = errors.New("claude: subagent record coverage gap")

// SubagentQuery bounds a fresh local read, not native pagination. Zero selects
// 10000 agents, 64 MiB total file bytes, 100000 JSON lines/directory entries,
// and 64 directory levels. Limits include main identity validation and sidecars.
type SubagentQuery struct {
	MaxRecords                     int
	MaxBytes                       int64
	MaxLines, MaxEntries, MaxDepth int
	// MaxFileBytes bounds optional metadata sampling per source (default 64 KiB).
	MaxFileBytes int64
}

// SubagentRecord is storage evidence only. Historical parent metadata does not
// grant membership, live relationship, execution identity or answer authority.
// Metadata and Sidecar retain unknown native fields and may contain private data.
type SubagentRecord struct {
	AgentID, Path, ToolUseID string
	ParentAgentID            *string
	Running                  string // Always "unknown": file existence is not execution evidence.
	Metadata                 []json.RawMessage
	Sidecar                  json.RawMessage
}
type DiscoveryIssue struct {
	Path, Reason string
	Err          error
}
type SubagentCoverage struct {
	// MainMetadata is only the sampled prefix, never a validated history chain.
	MainMetadata                              []json.RawMessage
	ScopeEvidence                             string
	Status                                    string // complete (declared layout only), partial, or unknown
	ConfigDir, CWD, SessionID, MainPath, Root string
	Directories                               []string
	Entries, Lines, Files                     int
	Bytes                                     int64
	Limits                                    SubagentQuery
	Issues                                    []DiscoveryIssue
	Atomic                                    bool // Always false: active native storage is not a snapshot.
}
type SubagentPage struct {
	Records  []SubagentRecord
	Coverage SubagentCoverage
}
type discoveryScope struct {
	config, cwd string
	err         error
}

// freezeDiscoveryScope captures the effective child environment and cwd at Open.
// It does not read transcripts or require that the config tree already exists.
func freezeDiscoveryScope(o *Options) discoveryScope {
	cwd := o.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, err := filepath.Abs(cwd)
	if err == nil {
		cwd, err = filepath.EvalSymlinks(cwd)
	}
	if err != nil {
		return discoveryScope{err: err}
	}
	o.WorkDir = cwd
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string(nil), env...)
	o.Env = env // Native process and discovery use the same frozen environment.
	value := func(key string) string {
		v := ""
		for _, e := range env {
			if strings.HasPrefix(e, key+"=") {
				v = strings.TrimPrefix(e, key+"=")
			}
		}
		return v
	}
	config := value("CLAUDE_CONFIG_DIR")
	if config == "" {
		home := value("HOME")
		if home == "" {
			return discoveryScope{cwd: cwd, err: fmt.Errorf("%w: effective HOME/config unknown", ErrInvalidInput)}
		}
		config = filepath.Join(home, ".claude")
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(cwd, config)
	}
	config = filepath.Clean(config)
	if resolved, e := filepath.EvalSymlinks(config); e == nil {
		config = resolved
	} else if !errors.Is(e, os.ErrNotExist) {
		return discoveryScope{config: config, cwd: cwd, err: e}
	}
	return discoveryScope{config: config, cwd: cwd}
}

// ListSubagents reads only this Session's native storage. It neither invokes the
// process nor changes observations. Complete means the supported directory walk
// completed; it never promises all native agents or an atomic/live inventory.
func (s *Session) ListSubagents(ctx context.Context, q SubagentQuery) (SubagentPage, error) {
	p := SubagentPage{Coverage: SubagentCoverage{Status: "unknown", ConfigDir: s.discovery.config, CWD: s.discovery.cwd}}
	fail := func(path, reason string, err error) {
		p.Coverage.Issues = append(p.Coverage.Issues, DiscoveryIssue{path, reason, err})
	}
	if ctx == nil {
		return p, fmt.Errorf("%w: nil context", ErrInvalidInput)
	}
	if q.MaxRecords < 0 || q.MaxBytes < 0 || q.MaxLines < 0 || q.MaxEntries < 0 || q.MaxDepth < 0 || q.MaxFileBytes < 0 {
		return p, fmt.Errorf("%w: negative discovery limit", ErrInvalidInput)
	}
	if q.MaxRecords == 0 {
		q.MaxRecords = 10000
	}
	if q.MaxBytes == 0 {
		q.MaxBytes = 64 << 20
	}
	if q.MaxLines == 0 {
		q.MaxLines = 100000
	}
	if q.MaxEntries == 0 {
		q.MaxEntries = 100000
	}
	if q.MaxDepth == 0 {
		q.MaxDepth = 64
	}
	if q.MaxFileBytes == 0 {
		q.MaxFileBytes = 64 << 10
	}
	p.Coverage.Limits = q
	s.mu.Lock()
	id, closed := s.root, s.closed
	s.mu.Unlock()
	p.Coverage.SessionID = id
	if closed {
		return p, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if s.discovery.err != nil {
		return p, s.discovery.err
	}
	if !historyUUID.MatchString(id) {
		return p, fmt.Errorf("%w: observed native main UUID required", ErrInvalidInput)
	}
	key := projectUnsafe.ReplaceAllString(s.discovery.cwd, "-")
	if len(key) > 200 {
		return p, fmt.Errorf("%w: truncated project keys unsupported", ErrUnsupported)
	}
	root, err := os.OpenRoot(s.discovery.config)
	if err != nil {
		fail(s.discovery.config, "config-access", err)
		return p, discoveryError(p)
	}
	defer root.Close()
	d := discoveryReader{ctx: ctx, session: s, root: root, page: &p, q: q}
	main := filepath.Join("projects", key, id+".jsonl")
	sub := filepath.Join("projects", key, id, "subagents")
	p.Coverage.MainPath = filepath.Join(s.discovery.config, main)
	p.Coverage.Root = filepath.Join(s.discovery.config, sub)
	// Check all in-tree components; symlinks are not part of the native layout.
	for _, path := range []string{"projects", filepath.Join("projects", key)} {
		if err = d.directory(path); err != nil {
			fail(path, "scope-access", err)
			return p, discoveryError(p)
		}
	}
	// The exact native path and observed root identity select the directory.
	// Main payload is optional metadata: incomplete/large history is not a gate.
	p.Coverage.ScopeEvidence = "observed-main-uuid+exact-project-path"
	lines, readErr := d.read(main)
	p.Coverage.MainMetadata = lines
	if readErr != nil {
		fail(main, "main-metadata-incomplete", readErr)
	}
	for _, raw := range lines {
		if validateDiscoveryJSON(raw) != nil {
			continue
		}
		var fields map[string]json.RawMessage
		if e := json.Unmarshal(raw, &fields); e != nil {
			continue
		}
		mainID, idErr := discoveryString(fields, "sessionId")
		cwd, cwdErr := discoveryString(fields, "cwd")
		if e := errors.Join(idErr, cwdErr); e != nil {
			fail(main, "main-identity-metadata-invalid", e)
			continue
		}
		if (mainID != nil && *mainID != "" && *mainID != id) || (cwd != nil && *cwd != "" && *cwd != s.discovery.cwd) {
			fail(main, "main-scope-conflict", ErrDiscoveryCoverage)
			return p, discoveryError(p)
		}
	}
	p.Coverage.Status = "complete"
	for _, path := range []string{filepath.Join("projects", key, id), sub} {
		if err = d.directory(path); err != nil {
			fail(path, "layout-missing-or-inaccessible", err)
			p.Coverage.Status = "partial"
			return p, discoveryError(p)
		}
	}
	d.walk(sub, 0)
	sort.Slice(p.Records, func(i, j int) bool { return p.Records[i].Path < p.Records[j].Path })
	if len(p.Coverage.Issues) > 0 {
		p.Coverage.Status = "partial"
		return p, discoveryError(p)
	}
	return p, nil
}
func discoveryError(p SubagentPage) error {
	errs := []error{ErrDiscoveryCoverage}
	for _, i := range p.Coverage.Issues {
		errs = append(errs, fmt.Errorf("%s: %s: %w", i.Path, i.Reason, i.Err))
	}
	return errors.Join(errs...)
}

type discoveryReader struct {
	ctx     context.Context
	session *Session
	root    *os.Root
	page    *SubagentPage
	q       SubagentQuery
	stopped bool
}

func (d *discoveryReader) issue(path, reason string, err error) {
	d.page.Coverage.Issues = append(d.page.Coverage.Issues, DiscoveryIssue{path, reason, err})
}
func (d *discoveryReader) check() error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	select {
	case <-d.session.Done():
		return ErrClosed
	default:
		return nil
	}
}
func (d *discoveryReader) limit(resource string, n int) error {
	return &ResourceLimitError{Resource: "discovery " + resource, Limit: n}
}

// openDirectory pins each ordinary component before accessing its children.
// SameFile rejects a symlink substitution between Lstat and OpenRoot; all later
// operations use the pinned handle, not the mutable path in the config tree.
func (d *discoveryReader) openDirectory(path string) (*os.Root, error) {
	current, err := d.root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if err = d.check(); err != nil {
			current.Close()
			return nil, err
		}
		st, e := current.Lstat(part)
		if e != nil {
			current.Close()
			return nil, e
		}
		if !st.IsDir() {
			current.Close()
			return nil, errors.New("not an ordinary directory")
		}
		next, e := current.OpenRoot(part)
		if e != nil {
			current.Close()
			return nil, e
		}
		opened, e := next.Lstat(".")
		current.Close()
		if e != nil || !os.SameFile(st, opened) {
			next.Close()
			return nil, errors.New("directory changed during open")
		}
		current = next
	}
	return current, nil
}
func (d *discoveryReader) directory(path string) error {
	r, err := d.openDirectory(path)
	if err == nil {
		r.Close()
	}
	return err
}
func (d *discoveryReader) read(path string) ([]json.RawMessage, error) {
	if err := d.check(); err != nil {
		return nil, err
	}
	parent, err := d.openDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := filepath.Base(path)
	st, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not an ordinary file")
	}
	available := min(d.q.MaxBytes-d.page.Coverage.Bytes, d.q.MaxFileBytes)
	if available <= 0 {
		return nil, d.limit("metadata bytes", int(d.q.MaxBytes))
	}
	f, err := openDiscoveryFile(parent, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		return nil, errors.New("file changed during open")
	}
	size := min(st.Size(), available)
	data, err := io.ReadAll(io.NewSectionReader(f, 0, size))
	d.page.Coverage.Bytes += int64(len(data))
	d.page.Coverage.Files++
	if err != nil {
		return nil, err
	}
	var resultErr error
	if size < st.Size() {
		resultErr = d.limit("metadata bytes per file/total", int(available))
	}
	if int64(len(data)) != size {
		resultErr = errors.Join(resultErr, io.ErrUnexpectedEOF)
	}
	var lines []json.RawMessage
	records := bytes.Split(data, []byte{'\n'})
	if strings.HasSuffix(path, ".meta.json") {
		records = [][]byte{data}
	}
	for _, raw := range records {
		if err = d.check(); err != nil {
			return lines, errors.Join(resultErr, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if d.page.Coverage.Lines >= d.q.MaxLines {
			return lines, errors.Join(resultErr, d.limit("metadata lines", d.q.MaxLines))
		}
		d.page.Coverage.Lines++
		lines = append(lines, append(json.RawMessage(nil), raw...))
		var obj map[string]json.RawMessage
		if err = validateDiscoveryJSON(raw); err != nil {
			resultErr = errors.Join(resultErr, err)
		} else if err = json.Unmarshal(raw, &obj); err != nil {
			resultErr = errors.Join(resultErr, err)
		} else if obj == nil {
			resultErr = errors.Join(resultErr, errors.New("metadata is not an object"))
		}
	}
	return lines, resultErr
}
func (d *discoveryReader) walk(path string, depth int) {
	if d.stopped {
		return
	}
	if err := d.check(); err != nil {
		d.issue(path, "canceled-or-closed", err)
		d.stopped = true
		return
	}
	if depth > d.q.MaxDepth {
		d.issue(path, "depth-limit", d.limit("depth", d.q.MaxDepth))
		return
	}
	if err := d.directory(path); err != nil {
		d.issue(path, "directory-access", err)
		return
	}
	dir, err := d.openDirectory(path)
	if err != nil {
		d.issue(path, "directory-access", err)
		return
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		d.issue(path, "directory-access", err)
		return
	}
	defer f.Close()
	d.page.Coverage.Directories = append(d.page.Coverage.Directories, filepath.Join(d.page.Coverage.ConfigDir, path))
	for !d.stopped {
		entries, readErr := f.ReadDir(1)
		if len(entries) == 0 && readErr == io.EOF {
			return
		}
		if readErr != nil && readErr != io.EOF {
			d.issue(path, "directory-read", readErr)
			return
		}
		for _, e := range entries {
			if err := d.check(); err != nil {
				d.issue(path, "canceled-or-closed", err)
				d.stopped = true
				return
			}
			if d.page.Coverage.Entries >= d.q.MaxEntries {
				d.issue(path, "entry-limit", d.limit("entries", d.q.MaxEntries))
				d.stopped = true
				return
			}
			d.page.Coverage.Entries++
			child := filepath.Join(path, e.Name())
			if e.IsDir() {
				d.walk(child, depth+1)
				continue
			}
			if !e.Type().IsRegular() {
				d.issue(child, "unsupported-layout", errors.New("non-regular entry"))
				continue
			}
			if strings.HasPrefix(e.Name(), "agent-") && strings.HasSuffix(e.Name(), ".jsonl") {
				if len(d.page.Records) >= d.q.MaxRecords {
					d.issue(child, "record-limit", d.limit("records", d.q.MaxRecords))
					d.stopped = true
					return
				}
				d.agent(child, e.Name())
				continue
			}
			if strings.HasPrefix(e.Name(), "agent-") && strings.HasSuffix(e.Name(), ".meta.json") {
				transcript := strings.TrimSuffix(child, ".meta.json") + ".jsonl"
				if _, err := d.root.Lstat(transcript); err != nil {
					d.issue(child, "orphan-sidecar", err)
				}
				continue
			}
			d.issue(child, "unsupported-layout", ErrDiscoveryCoverage)
		}
	}
}
func (d *discoveryReader) agent(path, name string) {
	id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
	if id == "" || !utf8.ValidString(id) {
		d.issue(path, "empty-agent-id", ErrDiscoveryCoverage)
		return
	}
	// Filename/directory discovery survives incomplete or inaccessible payloads.
	r := SubagentRecord{AgentID: id, Path: filepath.Join(d.page.Coverage.ConfigDir, path), Running: "unknown"}
	meta := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	side, e := d.read(meta)
	for i, b := range side {
		if i > 0 {
			r.Sidecar = append(r.Sidecar, '\n')
		}
		r.Sidecar = append(r.Sidecar, b...)
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		d.issue(meta, "sidecar-access-or-parse", e)
	}
	if e == nil {
		if len(side) != 1 {
			d.issue(meta, "sidecar-shape", ErrDiscoveryCoverage)
		} else {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(side[0], &fields) // read already validated the object.
			tool, toolErr := discoveryString(fields, "toolUseId")
			parent, parentErr := discoveryString(fields, "parentAgentId")
			if e = errors.Join(toolErr, parentErr); e != nil {
				d.issue(meta, "sidecar-fields", e)
			} else {
				if tool != nil {
					r.ToolUseID = *tool
				}
				if parent != nil && *parent != "" {
					r.ParentAgentID = parent
				}
			}
		}
	}
	var err error
	r.Metadata, err = d.read(path)
	if err != nil {
		d.issue(path, "transcript-metadata-incomplete", err)
	}
	// Transcript sessionId, cwd, agentId and run extensions remain raw native
	// fields. No fixed source proves they are aliases of the selected main ID.
	for _, old := range d.page.Records {
		if old.AgentID == id {
			d.issue(path, "duplicate-agent-id", ErrDiscoveryCoverage)
			break
		}
	}
	d.page.Records = append(d.page.Records, r)
}

// Reuse the history module's pure duplicate-key/syntax validator, never its
// main-chain, terminal, replay or source-stability gates. encoding/json alone
// replaces invalid UTF-8 and unpaired UTF-16 escapes, so check those first.
func validateDiscoveryJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8 metadata")
	}
	if err := validateTranscriptJSON(raw); err != nil {
		return err
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return errors.New("unpaired UTF-16 low surrogate")
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return errors.New("unpaired UTF-16 high surrogate")
		}
		low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if e != nil || low < 0xdc00 || low > 0xdfff {
			return errors.New("invalid UTF-16 surrogate pair")
		}
		i += 6
	}
	return nil
}

// JS native keys are case-sensitive; encoding/json's struct matching is not.
func discoveryString(fields map[string]json.RawMessage, key string) (*string, error) {
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return &value, nil
}
