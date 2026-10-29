package claude

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SubagentHistoryLimits bounds the entire independent local query, not each page.
// Zero selects the defaults recorded in the returned Coverage.Limits.
type SubagentHistoryLimits struct {
	MaxBytes                           int64
	MaxLines, MaxRecords, MaxLineBytes int
	MaxFiles, MaxDirectories, MaxDepth int
	MaxSidecarBytes                    int64
}
type ListSubagentsOptions struct {
	Scope                    SessionQueryScope
	Limits                   SubagentHistoryLimits
	Limit, Offset, MaxAgents int
}
type SubagentMessagesOptions struct {
	Scope                      SessionQueryScope
	Limits                     SubagentHistoryLimits
	Limit, Offset, MaxMessages int
}
type SubagentInfo struct {
	SessionID, AgentID, Path       string
	ParentAgentID, ParentToolUseID *string
	Sidecar                        json.RawMessage
	Running                        string // Always unknown: saved records do not establish live state.
}
type SubagentMessage struct {
	SessionMessage
	AgentID                        string
	ParentAgentID, ParentToolUseID *string
}
type SubagentHistoryCoverage struct {
	Status                                                          string
	ConfigDir, ProjectDir, SessionID, AgentID, MainPath, Root, Path string
	Directories                                                     []string
	Entries, Files, Lines, Records                                  int
	Bytes                                                           int64
	Atomic                                                          bool
	Limits                                                          SubagentHistoryLimits
	MaxEntries, Limit, Offset, MaxResults                           int
	SidecarStatus                                                   string
}
type SubagentsPage struct {
	Records  []SubagentInfo
	Coverage SubagentHistoryCoverage
}
type SubagentMessagesPage struct {
	Info     SubagentInfo
	Messages []SubagentMessage
	Coverage SubagentHistoryCoverage
}

type childDirectory struct {
	root    *os.Root
	path    string
	info    os.FileInfo
	entries map[string]os.FileInfo
}
type childSource struct {
	parent     *childDirectory
	name, path string
	file       *os.File
	info       os.FileInfo
	hash       [32]byte
}
type childCandidate struct {
	agent  string
	parent *childDirectory
	name   string
	info   os.FileInfo
}
type childQuery struct {
	ctx     context.Context
	scope   SessionQueryScope
	session string
	c       *SubagentHistoryCoverage
	root    *os.Root
	dirs    []*childDirectory
	files   []childSource
	absent  []struct {
		parent *childDirectory
		name   string
	}
}

// ListSubagents discovers saved children without a Session, environment lookup,
// process, history replay or live membership/answer authority. Any error clears Records.
func ListSubagents(ctx context.Context, parentSessionID string, o ListSubagentsOptions) (page SubagentsPage, err error) {
	page.Records = []SubagentInfo{}
	page.Coverage = SubagentHistoryCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, SessionID: parentSessionID, SidecarStatus: "per-record"}
	defer func() {
		if err != nil {
			page.Records = []SubagentInfo{}
		}
	}()
	q, err := newChildQuery(ctx, parentSessionID, "", o.Scope, o.Limits, o.Limit, o.Offset, o.MaxAgents, 100, &page.Coverage)
	if err != nil {
		return page, err
	}
	defer q.close()
	candidates, err := q.inventory()
	if err != nil {
		return page, err
	}
	if len(candidates) > q.c.MaxResults {
		return page, queryError("resource-limit", q.c.Root, "MaxAgents", nil)
	}
	for _, candidate := range candidates {
		info, _, e := q.load(candidate, false)
		if e != nil {
			return page, e
		}
		page.Records = append(page.Records, info)
	}
	if err = q.verify(); err != nil {
		return page, err
	}
	start := min(q.c.Offset, len(page.Records))
	end := start + min(q.c.Limit, len(page.Records)-start)
	page.Records = page.Records[start:end]
	q.c.Status = "complete"
	return page, nil
}

// GetSubagentMessages selects the child's own final-leaf parentUuid chain. It
// never runs main-history compaction/parallel recovery or opens a live Session.
// Any error clears Info and Messages, retaining only actual Coverage.
func GetSubagentMessages(ctx context.Context, parentSessionID, agentID string, o SubagentMessagesOptions) (page SubagentMessagesPage, err error) {
	page.Messages = []SubagentMessage{}
	page.Coverage = SubagentHistoryCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, SessionID: parentSessionID, AgentID: agentID}
	defer func() {
		if err != nil {
			page.Info = SubagentInfo{}
			page.Messages = []SubagentMessage{}
		}
	}()
	if !safeChildID(agentID) {
		return page, queryError("invalid-input", "", "invalid native agentID", ErrInvalidInput)
	}
	q, err := newChildQuery(ctx, parentSessionID, agentID, o.Scope, o.Limits, o.Limit, o.Offset, o.MaxMessages, 1000, &page.Coverage)
	if err != nil {
		return page, err
	}
	defer q.close()
	candidates, err := q.inventory()
	if err != nil {
		return page, err
	}
	var chosen *childCandidate
	for i := range candidates {
		if candidates[i].agent == agentID {
			chosen = &candidates[i]
		}
	}
	if chosen == nil {
		return page, queryError("missing", q.c.Root, "saved child not found", os.ErrNotExist)
	}
	var records []SessionMessage
	page.Info, records, err = q.load(*chosen, true)
	if err != nil {
		return page, err
	}
	if len(records) > q.c.MaxResults {
		return page, queryError("resource-limit", page.Info.Path, "MaxMessages", nil)
	}
	start := min(q.c.Offset, len(records))
	end := start + min(q.c.Limit, len(records)-start)
	for _, r := range records[start:end] {
		page.Messages = append(page.Messages, SubagentMessage{SessionMessage: r, AgentID: agentID, ParentAgentID: cloneChildString(page.Info.ParentAgentID), ParentToolUseID: cloneChildString(page.Info.ParentToolUseID)})
	}
	if err = q.verify(); err != nil {
		return page, err
	}
	q.c.Status = "complete"
	return page, nil
}

func safeChildID(id string) bool {
	if !utf8.ValidString(id) || len(id) == 0 || len(id) > 255 || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\:") && !strings.ContainsFunc(id, unicode.IsControl)
}
func cloneChildString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func newChildQuery(ctx context.Context, session, agent string, scope SessionQueryScope, limits SubagentHistoryLimits, limit, offset, maxResults, defaultLimit int, c *SubagentHistoryCoverage) (*childQuery, error) {
	if ctx == nil {
		return nil, queryError("invalid-input", "", "nil context", ErrInvalidInput)
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !historyUUID.MatchString(session) || !filepath.IsAbs(scope.ConfigDir) || (scope.ProjectDir != "" && !filepath.IsAbs(scope.ProjectDir)) {
		return nil, queryError("invalid-input", "", "native parent UUID and explicit absolute scope required", ErrInvalidInput)
	}
	if limit < 0 || offset < 0 || maxResults < 0 || scope.MaxEntries < 0 || limits.MaxBytes < 0 || limits.MaxSidecarBytes < 0 {
		return nil, queryError("invalid-input", "", "negative limit", ErrInvalidInput)
	}
	for _, n := range []int{limits.MaxLines, limits.MaxRecords, limits.MaxLineBytes, limits.MaxFiles, limits.MaxDirectories, limits.MaxDepth} {
		if n < 0 {
			return nil, queryError("invalid-input", "", "negative limit", ErrInvalidInput)
		}
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if maxResults == 0 {
		maxResults = 10000
	}
	if scope.MaxEntries == 0 {
		scope.MaxEntries = 10000
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = 64 << 20
	}
	if limits.MaxSidecarBytes == 0 {
		limits.MaxSidecarBytes = 64 << 10
	}
	for _, d := range []struct {
		p *int
		n int
	}{{&limits.MaxLines, 100000}, {&limits.MaxRecords, 100000}, {&limits.MaxLineBytes, 8 << 20}, {&limits.MaxFiles, 10000}, {&limits.MaxDirectories, 10000}, {&limits.MaxDepth, 64}} {
		if *d.p == 0 {
			*d.p = d.n
		}
	}
	c.Limits = limits
	c.MaxEntries = scope.MaxEntries
	c.Limit = limit
	c.Offset = offset
	c.MaxResults = maxResults
	if limit > maxResults {
		return nil, queryError("resource-limit", "", "requested result limit exceeds maximum", nil)
	}
	return &childQuery{ctx: ctx, session: session, scope: scope, c: c}, nil
}
func (q *childQuery) close() {
	for _, f := range q.files {
		_ = f.file.Close()
	}
	for i := len(q.dirs) - 1; i >= 0; i-- {
		_ = q.dirs[i].root.Close()
	}
	if q.root != nil {
		_ = q.root.Close()
	}
}
func (q *childQuery) directory(parent *os.Root, name, path string) (*childDirectory, error) {
	if len(q.dirs) >= q.c.Limits.MaxDirectories {
		return nil, queryError("resource-limit", path, "directories", nil)
	}
	r, e := queryDirectory(parent, name)
	if e != nil {
		return nil, e
	}
	info, e := r.Stat(".")
	if e != nil {
		r.Close()
		return nil, queryAccess(path, e)
	}
	d := &childDirectory{root: r, path: path, info: info}
	q.dirs = append(q.dirs, d)
	q.c.Directories = append(q.c.Directories, filepath.Join(q.scope.ConfigDir, path))
	return d, nil
}
func (q *childQuery) scan(d *childDirectory) (map[string]os.FileInfo, error) {
	f, e := d.root.Open(".")
	if e != nil {
		return nil, queryAccess(d.path, e)
	}
	defer f.Close()
	entries := map[string]os.FileInfo{}
	for {
		if e = q.ctx.Err(); e != nil {
			return nil, e
		}
		batch, readErr := f.ReadDir(1)
		for _, entry := range batch {
			if q.c.Entries >= q.c.MaxEntries {
				return nil, queryError("resource-limit", d.path, "directory entries", nil)
			}
			q.c.Entries++
			info, e := d.root.Lstat(entry.Name())
			if e != nil {
				return nil, queryError("changed", d.path, "entry disappeared", e)
			}
			entries[entry.Name()] = info
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, queryAccess(d.path, readErr)
		}
	}
	d.entries = entries
	return entries, nil
}
func (q *childQuery) optionalDir(parent *childDirectory, name string) (*childDirectory, error) {
	_, e := parent.root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		q.absent = append(q.absent, struct {
			parent *childDirectory
			name   string
		}{parent, name})
		return nil, nil
	}
	if e != nil {
		return nil, queryAccess(parent.path, e)
	}
	return q.directory(parent.root, name, filepath.Join(parent.path, name))
}
func (q *childQuery) inventory() ([]childCandidate, error) {
	root, projects, key, e := openQueryScope(q.scope)
	if e != nil {
		return nil, e
	}
	q.root = root
	// Reopen through the owned root to include its directory identity in stability checks.
	projects.Close()
	pd, e := q.directory(root, "projects", "projects")
	if e != nil {
		return nil, e
	}
	var project *childDirectory
	if key != "" {
		project, e = q.directory(pd.root, key, filepath.Join("projects", key))
		if e != nil {
			return nil, e
		}
	} else {
		entries, e := q.scan(pd)
		if e != nil {
			return nil, e
		}
		names := childSortedNames(entries)
		for _, name := range names {
			d, e := q.directory(pd.root, name, filepath.Join("projects", name))
			if e != nil {
				return nil, e
			}
			_, e = d.root.Lstat(q.session + ".jsonl")
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return nil, queryAccess(d.path, e)
			}
			if project != nil {
				return nil, queryError("ambiguous", q.session, "parent in multiple projects", nil)
			}
			project = d
		}
	}
	if project == nil {
		return nil, queryError("missing", q.session, "parent transcript not found", os.ErrNotExist)
	}
	if _, e = q.scan(project); e != nil {
		return nil, e
	}
	q.c.MainPath = filepath.Join(q.scope.ConfigDir, project.path, q.session+".jsonl")
	q.c.Status = "partial"
	data, e := q.read(project, q.session+".jsonl", false)
	if e != nil {
		return nil, e
	}
	if _, e = q.parse(data, q.c.MainPath, "", false); e != nil {
		return nil, e
	}
	container, e := q.optionalDir(project, q.session)
	if e != nil {
		return nil, e
	}
	if container == nil {
		return []childCandidate{}, nil
	}
	sub, e := q.optionalDir(container, "subagents")
	if e != nil {
		return nil, e
	}
	if sub == nil {
		return []childCandidate{}, nil
	}
	q.c.Root = filepath.Join(q.scope.ConfigDir, sub.path)
	candidates := []childCandidate{}
	var walk func(*childDirectory, int) error
	walk = func(d *childDirectory, depth int) error {
		if depth > q.c.Limits.MaxDepth {
			return queryError("resource-limit", d.path, "directory depth", nil)
		}
		entries, e := q.scan(d)
		if e != nil {
			return e
		}
		for _, name := range childSortedNames(entries) {
			info := entries[name]
			switch {
			case info.IsDir():
				if depth >= q.c.Limits.MaxDepth {
					return queryError("resource-limit", d.path, "directory depth", nil)
				}
				next, e := q.directory(d.root, name, filepath.Join(d.path, name))
				if e != nil {
					return e
				}
				if e = walk(next, depth+1); e != nil {
					return e
				}
			case !info.Mode().IsRegular():
				return queryError("unsupported", filepath.Join(d.path, name), "non-regular child entry", ErrUnsupported)
			case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl"):
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
				if !safeChildID(id) {
					return queryError("corrupt", filepath.Join(d.path, name), "invalid filename agentID", nil)
				}
				bound := q.c.MaxEntries
				if q.c.AgentID == "" {
					bound = q.c.MaxResults
				}
				if len(candidates) >= bound {
					return queryError("resource-limit", d.path, "child inventory", nil)
				}
				for _, old := range candidates {
					if id == old.agent || os.SameFile(info, old.info) {
						return queryError("ambiguous", filepath.Join(d.path, name), "duplicate child identity or file alias", nil)
					}
				}
				candidates = append(candidates, childCandidate{id, d, name, info})
			case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".meta.json"):
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
				if !safeChildID(id) {
					return queryError("corrupt", filepath.Join(d.path, name), "invalid sidecar agentID", nil)
				}
				if _, ok := entries["agent-"+id+".jsonl"]; !ok {
					return queryError("corrupt", filepath.Join(d.path, name), "orphan sidecar", nil)
				}
			default:
				return queryError("unsupported", filepath.Join(d.path, name), "unknown child layout", ErrUnsupported)
			}
		}
		return nil
	}
	if e = walk(sub, 0); e != nil {
		return nil, e
	}
	sort.Slice(candidates, func(i, j int) bool {
		return filepath.Join(candidates[i].parent.path, candidates[i].name) < filepath.Join(candidates[j].parent.path, candidates[j].name)
	})
	return candidates, nil
}
func childSortedNames(entries map[string]os.FileInfo) []string {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (q *childQuery) read(parent *childDirectory, name string, sidecar bool) ([]byte, error) {
	relative := filepath.Join(parent.path, name)
	path := filepath.Join(q.scope.ConfigDir, relative)
	if q.c.Files >= q.c.Limits.MaxFiles {
		return nil, queryError("resource-limit", path, "files read", nil)
	}
	st, e := parent.root.Lstat(name)
	if e != nil {
		return nil, queryAccess(path, e)
	}
	if !st.Mode().IsRegular() {
		return nil, queryError("unsupported", path, "non-regular source", ErrUnsupported)
	}
	if original, ok := parent.entries[name]; ok && (!os.SameFile(original, st) || original.Size() != st.Size() || !original.ModTime().Equal(st.ModTime())) {
		return nil, queryError("changed", path, "source changed after inventory", nil)
	}
	limit := q.c.Limits.MaxBytes - q.c.Bytes
	if sidecar {
		limit = min(limit, q.c.Limits.MaxSidecarBytes, int64(q.c.Limits.MaxLineBytes))
	}
	if st.Size() > limit {
		return nil, queryError("resource-limit", path, "source bytes", nil)
	}
	f, e := openDiscoveryFile(parent.root, name)
	if e != nil {
		if errors.Is(e, ErrUnsupported) {
			return nil, queryError("unsupported", path, "local query platform", e)
		}
		return nil, queryAccess(path, e)
	}
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, queryAccess(path, e)
	}
	if !os.SameFile(st, info) || !info.Mode().IsRegular() || info.Size() != st.Size() {
		f.Close()
		return nil, queryError("changed", path, "source replaced during open", nil)
	}
	data, e := readQueryRange(q.ctx, f, 0, info.Size())
	if e != nil {
		f.Close()
		return nil, e
	}
	q.c.Bytes += int64(len(data))
	q.c.Files++
	q.files = append(q.files, childSource{parent, name, relative, f, info, sha256.Sum256(data)})
	return data, nil
}
func (q *childQuery) identity(fields map[string]json.RawMessage, agent, path string) error {
	for key, want := range map[string]string{"sessionId": q.session, "session_id": q.session, "agentId": agent, "agent_id": agent} {
		if _, ok := fields[key]; !ok {
			continue
		}
		value, e := discoveryString(fields, key)
		if e != nil || value == nil || *value != want {
			return queryError("corrupt", path, "source identity mismatch: "+key, nil)
		}
	}
	return nil
}
func (q *childQuery) parse(data []byte, path, agent string, chain bool) ([]SessionMessage, error) {
	records := []SessionMessage{}
	index := map[string]int{}
	leaf := -1
	scan := transcriptScanner(bytes.NewReader(data), q.c.Limits.MaxLineBytes)
	for scan.Scan() {
		if e := q.ctx.Err(); e != nil {
			return nil, e
		}
		if q.c.Lines >= q.c.Limits.MaxLines {
			return nil, queryError("resource-limit", path, "lines", nil)
		}
		q.c.Lines++
		raw := scan.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		bad := func(kind, reason string) error {
			return &SessionQueryError{Kind: kind, Path: path, Reason: reason, Raw: bytes.Clone(raw)}
		}
		if validateDiscoveryJSON(raw) != nil {
			return nil, bad("corrupt", "invalid JSON object")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("corrupt", "object required")
		}
		if e := q.identity(fields, agent, path); e != nil {
			if qe, ok := e.(*SessionQueryError); ok {
				qe.Raw = bytes.Clone(raw)
			}
			return nil, e
		}
		kind, e := discoveryString(fields, "type")
		if e != nil || kind == nil {
			return nil, bad("corrupt", "invalid type")
		}
		switch *kind {
		case "user", "assistant", "progress", "system", "attachment":
		default:
			if transcriptMetadata(*kind) || *kind == "api-request-shape" || *kind == "api-request-blob" || *kind == "api-request" || *kind == "cost-state" {
				continue
			}
			return nil, bad("unsupported", "unknown entry type")
		}
		if q.c.Records >= q.c.Limits.MaxRecords {
			return nil, bad("resource-limit", "indexed records")
		}
		q.c.Records++
		if !chain {
			continue
		}
		id, e := discoveryString(fields, "uuid")
		if e != nil || id == nil || *id == "" {
			return nil, bad("corrupt", "missing UUID")
		}
		if _, exists := index[*id]; exists {
			return nil, bad("corrupt", "duplicate UUID")
		}
		parent, e := discoveryString(fields, "parentUuid")
		if e != nil || (parent != nil && *parent == "") {
			return nil, bad("corrupt", "invalid parentUuid")
		}
		stamp, e := discoveryString(fields, "timestamp")
		if e != nil {
			return nil, bad("corrupt", "invalid timestamp")
		}
		if *kind == "user" || *kind == "assistant" {
			var message map[string]json.RawMessage
			if json.Unmarshal(fields["message"], &message) != nil || message == nil {
				return nil, bad("corrupt", "invalid native message")
			}
			leaf = len(records)
		}
		index[*id] = len(records)
		records = append(records, SessionMessage{Type: *kind, UUID: *id, SessionID: q.session, ParentUUID: parent, Timestamp: stamp, Message: bytes.Clone(fields["message"]), Raw: bytes.Clone(raw)})
	}
	if e := scan.Err(); e != nil {
		if errors.Is(e, bufio.ErrTooLong) {
			return nil, queryError("resource-limit", path, "line bytes", e)
		}
		return nil, queryError("corrupt", path, "incomplete or oversized JSONL", e)
	}
	// Check every branch, including records outside the selected leaf's chain.
	done := map[string]bool{}
	for _, r := range records {
		visiting := map[string]bool{}
		id := r.UUID
		for id != "" && !done[id] {
			if visiting[id] {
				return nil, queryError("corrupt", path, "child parent cycle", nil)
			}
			visiting[id] = true
			pos, ok := index[id]
			if !ok {
				return nil, queryError("corrupt", path, "missing child parent", nil)
			}
			next := records[pos].ParentUUID
			if next == nil {
				break
			}
			id = *next
		}
		for id := range visiting {
			done[id] = true
		}
	}
	if leaf < 0 {
		return []SessionMessage{}, nil
	}
	selected := []SessionMessage{}
	for i := leaf; ; {
		r := records[i]
		if r.Type == "user" || r.Type == "assistant" {
			selected = append(selected, r)
		}
		if r.ParentUUID == nil {
			break
		}
		i = index[*r.ParentUUID]
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected, nil
}
func (q *childQuery) load(candidate childCandidate, chain bool) (SubagentInfo, []SessionMessage, error) {
	path := filepath.Join(q.scope.ConfigDir, candidate.parent.path, candidate.name)
	q.c.Path = path
	info := SubagentInfo{SessionID: q.session, AgentID: candidate.agent, Path: path, Running: "unknown"}
	data, e := q.read(candidate.parent, candidate.name, false)
	if e != nil {
		return info, nil, e
	}
	records, e := q.parse(data, path, candidate.agent, chain)
	if e != nil {
		return info, nil, e
	}
	name := "agent-" + candidate.agent + ".meta.json"
	metaPath := filepath.Join(q.scope.ConfigDir, candidate.parent.path, name)
	_, e = candidate.parent.root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		q.absent = append(q.absent, struct {
			parent *childDirectory
			name   string
		}{candidate.parent, name})
		if q.c.AgentID != "" {
			q.c.SidecarStatus = "absent"
		}
		return info, records, nil
	}
	if e != nil {
		return info, nil, queryAccess(path, e)
	}
	data, e = q.read(candidate.parent, name, true)
	if e != nil {
		return info, nil, e
	}
	if q.c.AgentID != "" {
		q.c.SidecarStatus = "present"
	}
	lines := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	if lines > q.c.Limits.MaxLines-q.c.Lines {
		return info, nil, queryError("resource-limit", path, "sidecar lines", nil)
	}
	q.c.Lines += lines
	if validateDiscoveryJSON(data) != nil {
		return info, nil, &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "invalid sidecar JSON", Raw: bytes.Clone(data)}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return info, nil, &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "sidecar object required", Raw: bytes.Clone(data)}
	}
	if e = q.identity(fields, candidate.agent, metaPath); e != nil {
		return info, nil, e
	}
	for key, dest := range map[string]**string{"toolUseId": &info.ParentToolUseID, "parentAgentId": &info.ParentAgentID} {
		value, e := discoveryString(fields, key)
		if e != nil || (value != nil && (*value == "" || (key == "parentAgentId" && (!safeChildID(*value) || *value == candidate.agent)))) {
			return info, nil, &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "invalid sidecar parent", Raw: bytes.Clone(data), Cause: e}
		}
		*dest = value
	}
	info.Sidecar = bytes.Clone(data)
	return info, records, nil
}
func (q *childQuery) verify() error {
	for _, d := range q.dirs {
		if e := q.ctx.Err(); e != nil {
			return e
		}
		now, e := q.root.Lstat(d.path)
		if e != nil || !now.IsDir() || !os.SameFile(d.info, now) {
			return queryError("changed", d.path, "directory replaced", e)
		}
		if d.entries == nil {
			continue
		}
		f, e := d.root.Open(".")
		if e != nil {
			return queryError("changed", d.path, "directory inaccessible", e)
		}
		count := 0
		for {
			batch, re := f.ReadDir(1)
			for _, entry := range batch {
				old, ok := d.entries[entry.Name()]
				if !ok {
					f.Close()
					return queryError("changed", d.path, "entry added", nil)
				}
				now, e := d.root.Lstat(entry.Name())
				if e != nil || !os.SameFile(old, now) || old.Mode() != now.Mode() {
					f.Close()
					return queryError("changed", d.path, "entry replaced", e)
				}
				count++
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				f.Close()
				return queryError("changed", d.path, "directory changed", re)
			}
			if e = q.ctx.Err(); e != nil {
				f.Close()
				return e
			}
		}
		f.Close()
		if count != len(d.entries) {
			return queryError("changed", d.path, "entry removed", nil)
		}
	}
	for _, f := range q.files {
		if e := verifyQuerySource(q.ctx, f.file, q.root, f.path, f.info, f.hash); e != nil {
			return e
		}
	}
	for _, missing := range q.absent {
		if _, e := missing.parent.root.Lstat(missing.name); !errors.Is(e, os.ErrNotExist) {
			return queryError("changed", missing.parent.path, "optional source appeared/changed", e)
		}
	}
	return q.ctx.Err()
}
