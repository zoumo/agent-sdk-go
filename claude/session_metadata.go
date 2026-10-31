package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SessionInfoOptions selects the same local layout as GetSessionMessages.
// Zero defaults: SampleBytes=64 KiB per head/tail, MaxBytes=16 MiB total,
// MaxLines=10000 sampled lines. MaxBytes includes the optional 1-byte tail
// boundary probe. Negative values are invalid.
type SessionInfoOptions struct {
	Scope                 SessionQueryScope
	SampleBytes, MaxBytes int64
	MaxLines              int
}

// ListSessionsOptions bounds discovery before sorting and content slicing.
// Zero defaults: Limit=100, MaxSessions=10000, MaxPage=1000. Scope.MaxEntries
// defaults to 10000 and counts both project and immediate project entries.
// No worktree expansion or recursive transcript/subagent discovery is performed.
type ListSessionsOptions struct {
	Scope                               SessionQueryScope
	Limit, Offset, MaxSessions, MaxPage int
	SampleBytes, MaxBytes               int64
	MaxLines                            int
}

// SessionInfo is detached local metadata, not a live Session or full transcript.
// Head/Tail preserve exact sampled bytes (possibly partial edge lines); Records
// preserves complete sampled JSON objects, including unknown metadata. Sampled
// means the windows are separated, with only a boundary probe read in the gap.
// Neither value certifies a conversation chain.
type SessionInfo struct {
	SessionID, Path                                                   string
	LastModified                                                      time.Time
	FileSize                                                          int64
	Summary                                                           string
	CustomTitle, AITitle, FirstPrompt, GitBranch, Cwd, Tag, Timestamp *string
	CreatedAt                                                         *time.Time
	Running                                                           string // always unknown: a stored file does not establish liveness
	Sampled                                                           bool
	Head, Tail                                                        []byte
	Records                                                           []json.RawMessage
}

type SessionMetadataCoverage struct {
	Status, ConfigDir, ProjectDir string // unknown, partial, complete for declared discovery/sample scope only
	Entries, Files, Lines         int
	Bytes                         int64 // initial samples and optional 1-byte tail probe; verification rereads both
	Atomic                        bool  // always false, including across calls/pages
	Limits                        ListSessionsOptions
}
type SessionInfoResult struct {
	Info     *SessionInfo // filename/stat and readable metadata survive a partial sampling error
	Coverage SessionMetadataCoverage
}
type SessionsPage struct {
	Sessions []SessionInfo // partial records may accompany an error; inspect Coverage
	Coverage SessionMetadataCoverage
}

func metadataOptions(o ListSessionsOptions) (ListSessionsOptions, error) {
	if o.Limit < 0 || o.Offset < 0 || o.MaxSessions < 0 || o.MaxPage < 0 || o.SampleBytes < 0 || o.MaxBytes < 0 || o.MaxLines < 0 || o.Scope.MaxEntries < 0 {
		return o, queryError("invalid-input", "", "negative metadata limit/offset", ErrInvalidInput)
	}
	if o.Limit == 0 {
		o.Limit = 100
	}
	if o.MaxPage == 0 {
		o.MaxPage = 1000
	}
	if o.MaxSessions == 0 {
		o.MaxSessions = 10000
	}
	if o.SampleBytes == 0 {
		o.SampleBytes = 64 << 10
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 16 << 20
	}
	if o.MaxLines == 0 {
		o.MaxLines = 10000
	}
	if o.Scope.MaxEntries == 0 {
		o.Scope.MaxEntries = 10000
	}
	if o.Limit > o.MaxPage {
		return o, queryError("resource-limit", "", "requested metadata page exceeds MaxPage", nil)
	}
	return o, nil
}
func metadataCoverage(o ListSessionsOptions) SessionMetadataCoverage {
	return SessionMetadataCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, Limits: o}
}
func metadataContext(ctx context.Context) error {
	if ctx == nil {
		return queryError("invalid-input", "", "nil context", ErrInvalidInput)
	}
	return ctx.Err()
}

// GetSessionInfo locates one source through the message query's exact locator,
// then reads bounded head/tail samples. It never reads the entire history just
// to obtain metadata. Missing/ambiguous/errors are not successful empty results.
func GetSessionInfo(ctx context.Context, id string, options SessionInfoOptions) (result SessionInfoResult, err error) {
	o, e := metadataOptions(ListSessionsOptions{Scope: options.Scope, SampleBytes: options.SampleBytes, MaxBytes: options.MaxBytes, MaxLines: options.MaxLines})
	result.Coverage = metadataCoverage(o)
	if e != nil {
		return result, e
	}
	if e = metadataContext(ctx); e != nil {
		return result, e
	}
	if !historyUUID.MatchString(id) {
		return result, queryError("invalid-input", id, "native session UUID required", ErrInvalidInput)
	}
	root, projects, key, e := openQueryScope(o.Scope)
	if e != nil {
		return result, e
	}
	defer root.Close()
	defer projects.Close()
	location := SessionMessagesCoverage{}
	project, rel, e := locateQuerySource(ctx, projects, key, id+".jsonl", &location, o.Scope.MaxEntries)
	result.Coverage.Entries = location.Entries
	if e != nil {
		return result, e
	}
	defer project.Close()
	result.Coverage.Status = "partial"
	info, e := readSessionMetadata(ctx, root, project, rel, id, o, &result.Coverage)
	result.Info = info
	if e == nil {
		result.Coverage.Status = "complete"
	}
	return result, e
}

// ListSessions discovers immediate main transcripts in the explicit local scope.
// Results sort by lastModified descending, ties by path ascending (SDK policy).
// Duplicate IDs remain separate and report ambiguous; no native cursor or total
// is invented. Partial results and errors must be handled together.
func ListSessions(ctx context.Context, options ListSessionsOptions) (page SessionsPage, err error) {
	o, e := metadataOptions(options)
	page.Sessions = []SessionInfo{}
	page.Coverage = metadataCoverage(o)
	if e != nil {
		return page, e
	}
	if e = metadataContext(ctx); e != nil {
		return page, e
	}
	root, projects, key, e := openQueryScope(o.Scope)
	if e != nil {
		return page, e
	}
	defer root.Close()
	defer projects.Close()
	page.Coverage.Status = "partial"
	var problems []error
	seen := map[string]bool{}
	add := func(e error) {
		if e != nil {
			problems = append(problems, e)
		}
	}
	scan := func(key string) error {
		project, e := queryDirectory(projects, key)
		if e != nil {
			return e
		}
		defer project.Close()
		return metadataEntries(ctx, project, key, &page.Coverage, o.Scope.MaxEntries, func(entry os.DirEntry) error {
			name := entry.Name()
			id := strings.TrimSuffix(name, ".jsonl")
			if entry.IsDir() && historyUUID.MatchString(name) {
				return nil
			} // known per-session subagent storage; outside main scope
			if name == "sessions-index.json" && entry.Type().IsRegular() {
				return nil
			} // derived index, not authoritative metadata
			if !strings.HasSuffix(name, ".jsonl") || !historyUUID.MatchString(id) {
				add(queryError("unsupported", filepath.Join(key, name), "unknown project layout", ErrUnsupported))
				return nil
			}
			if len(page.Sessions) >= o.MaxSessions {
				return queryError("resource-limit", key, "metadata inventory", nil)
			}
			info, e := readSessionMetadata(ctx, root, project, filepath.Join(key, name), id, o, &page.Coverage)
			add(e)
			if info != nil {
				if seen[id] {
					add(queryError("ambiguous", info.Path, "duplicate session identity in project scope", nil))
				}
				seen[id] = true
				page.Sessions = append(page.Sessions, *info)
			}
			return ctx.Err()
		})
	}
	if key != "" {
		add(scan(key))
	} else {
		add(metadataEntries(ctx, projects, "projects", &page.Coverage, o.Scope.MaxEntries, func(entry os.DirEntry) error {
			if !entry.IsDir() {
				add(queryError("unsupported", entry.Name(), "non-directory project entry", ErrUnsupported))
				return nil
			}
			e := scan(entry.Name())
			add(e)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}))
	}
	sort.SliceStable(page.Sessions, func(i, j int) bool {
		a, b := page.Sessions[i], page.Sessions[j]
		if a.LastModified.Equal(b.LastModified) {
			return a.Path < b.Path
		}
		return a.LastModified.After(b.LastModified)
	})
	start := min(o.Offset, len(page.Sessions))
	end := start + min(o.Limit, len(page.Sessions)-start)
	page.Sessions = page.Sessions[start:end]
	err = errors.Join(problems...)
	if err == nil {
		page.Coverage.Status = "complete"
	}
	return page, err
}

func metadataEntries(ctx context.Context, root *os.Root, path string, c *SessionMetadataCoverage, max int, visit func(os.DirEntry) error) error {
	f, e := root.Open(".")
	if e != nil {
		return queryAccess(path, e)
	}
	defer f.Close()
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		entries, e := f.ReadDir(1)
		if len(entries) == 0 && e == io.EOF {
			return nil
		}
		if e != nil && e != io.EOF {
			return queryAccess(path, e)
		}
		for _, entry := range entries {
			if c.Entries >= max {
				return queryError("resource-limit", path, "discovery entries", nil)
			}
			c.Entries++
			if e = visit(entry); e != nil {
				return e
			}
		}
	}
}

func readSessionMetadata(ctx context.Context, root, project *os.Root, rel, id string, o ListSessionsOptions, c *SessionMetadataCoverage) (*SessionInfo, error) {
	path := filepath.Join(o.Scope.ConfigDir, "projects", rel)
	info := &SessionInfo{SessionID: id, Path: path, Running: "unknown", Records: []json.RawMessage{}}
	source, e := openQuerySource(ctx, root, project, filepath.Join("projects", rel), nil)
	if source.info != nil {
		info.LastModified = source.info.ModTime()
		info.FileSize = source.info.Size()
	}
	if e != nil {
		return info, e
	}
	defer source.close()
	sample, e := source.sample(o.SampleBytes, o.MaxBytes-c.Bytes)
	if e != nil {
		return info, e
	}
	head, tail := sample.head, sample.tail
	c.Bytes += int64(len(head) + len(tail))
	c.Files++
	info.Head = head
	info.Tail = tail
	info.Sampled = sample.sampled
	if info.Sampled {
		info.Tail = tail[1:]
	}
	var parseErr error
	headRecords := 0
	if !info.Sampled {
		headRecords, parseErr = parseSessionMetadata(ctx, info, append(bytes.Clone(head), tail...), true, true, len(head), o, c)
	} else {
		headRecords, parseErr = parseSessionMetadata(ctx, info, head, true, false, len(head), o, c)
		_, tailErr := parseSessionMetadata(ctx, info, tail, false, true, 0, o, c)
		parseErr = errors.Join(parseErr, tailErr)
	}
	parseErr = errors.Join(parseErr, extractSessionMetadata(ctx, info, headRecords))
	e = source.verify()
	if err := errors.Join(parseErr, e); err != nil {
		return info, err
	}
	// Only the first complete source record establishes a whole-file exclusion.
	// Later sidechain entries can coexist with a main conversation.
	if len(info.Records) > 0 && (!info.Sampled || headRecords > 0) {
		var first map[string]json.RawMessage
		_ = json.Unmarshal(info.Records[0], &first)
		team, _ := discoveryString(first, "teamName")
		if bytes.Equal(bytes.TrimSpace(first["isSidechain"]), []byte("true")) || (team != nil && *team != "") {
			return nil, nil
		}
	}
	return info, nil
}
func parseSessionMetadata(ctx context.Context, info *SessionInfo, data []byte, head, tail bool, headLimit int, o ListSessionsOptions, c *SessionMetadataCoverage) (headRecords int, err error) {
	if tail && len(data) > 0 && data[len(data)-1] != '\n' {
		return headRecords, queryError("corrupt", info.Path, "incomplete final metadata line", nil)
	}
	if !head {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	if !tail {
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			data = data[:i+1]
		} else {
			data = nil
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return headRecords, queryError("corrupt", info.Path, "incomplete final metadata line", nil)
	}
	offset := 0
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte{'\n'})
		data = rest
		offset += len(line) + 1
		if e := ctx.Err(); e != nil {
			return headRecords, e
		}
		if c.Lines >= o.MaxLines {
			return headRecords, queryError("resource-limit", info.Path, "sampled metadata lines", nil)
		}
		c.Lines++
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if e := validateDiscoveryJSON(line); e != nil {
			return headRecords, queryError("corrupt", info.Path, "invalid sampled metadata", e)
		}
		fields := map[string]json.RawMessage{}
		if e := json.Unmarshal(line, &fields); e != nil || fields == nil {
			return headRecords, queryError("corrupt", info.Path, "metadata object required", e)
		}
		info.Records = append(info.Records, bytes.Clone(line))
		if head && offset <= headLimit {
			headRecords++
		}
	}
	return headRecords, nil
}

func extractSessionMetadata(ctx context.Context, info *SessionInfo, headRecords int) error {
	var lastPrompt, summary *string
	command := ""
	for index, line := range info.Records {
		if e := ctx.Err(); e != nil {
			return e
		}
		// Provenance comes from parsed source offsets, never content equality.
		isHead := index < headRecords
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(line, &fields)

		for _, key := range []string{"isSidechain", "isMeta", "isCompactSummary"} {
			v := bytes.TrimSpace(fields[key])
			if len(v) > 0 && !bytes.Equal(v, []byte("null")) && !bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false")) {
				return queryError("corrupt", info.Path, "invalid sampled visibility flag", nil)
			}
		}
		if _, e := discoveryString(fields, "teamName"); e != nil {
			return queryError("corrupt", info.Path, "invalid sampled team name", e)
		}
		if sid, e := discoveryString(fields, "sessionId"); e != nil {
			return queryError("corrupt", info.Path, "sampled identity", e)
		} else if sid != nil && *sid != info.SessionID {
			return queryError("corrupt", info.Path, "conflicting sampled sessionId", nil)
		}
		for key, target := range map[string]**string{"customTitle": &info.CustomTitle, "aiTitle": &info.AITitle, "gitBranch": &info.GitBranch, "cwd": &info.Cwd} {
			v, e := discoveryString(fields, key)
			if e != nil {
				return queryError("corrupt", info.Path, "sampled metadata field", e)
			}
			if v != nil && *v != "" {
				if key != "cwd" || *target == nil {
					*target = v
				}
			}
		}
		kind, e := discoveryString(fields, "type")
		if e != nil {
			return queryError("corrupt", info.Path, "metadata type", e)
		}
		if kind != nil && *kind == "tag" {
			v, e := discoveryString(fields, "tag")
			if e != nil {
				return queryError("corrupt", info.Path, "tag", e)
			}
			info.Tag = v
		}
		if info.Timestamp == nil && isHead {
			v, e := discoveryString(fields, "timestamp")
			if e != nil {
				return queryError("corrupt", info.Path, "timestamp", e)
			}
			if v != nil {
				info.Timestamp = v
				if ts, e := time.Parse(time.RFC3339Nano, *v); e == nil {
					info.CreatedAt = &ts
				}
			}
		}
		if info.FirstPrompt == nil && isHead && kind != nil && *kind == "user" {
			for _, text := range metadataPromptTexts(fields) {
				if m := metadataCommand.FindStringSubmatch(text); len(m) > 0 {
					if command == "" {
						command = m[1]
					}
					continue
				}
				if metadataSkipPrompt.MatchString(text) {
					continue
				}
				r := []rune(text)
				if len(r) > 200 {
					text = strings.TrimRight(string(r[:200]), " ") + "…"
				}
				info.FirstPrompt = &text
				break
			}
		}

		for key, target := range map[string]**string{"lastPrompt": &lastPrompt, "summary": &summary} {
			v, e := discoveryString(fields, key)
			if e != nil {
				return queryError("corrupt", info.Path, "summary", e)
			}
			if v != nil && *v != "" {
				*target = v
			}
		}

	}
	if info.FirstPrompt == nil && command != "" {
		info.FirstPrompt = &command
	}
	switch {
	case info.CustomTitle != nil:
		info.Summary = *info.CustomTitle
	case info.AITitle != nil:
		info.Summary = *info.AITitle
	case lastPrompt != nil:
		info.Summary = *lastPrompt
	case summary != nil:
		info.Summary = *summary
	case info.FirstPrompt != nil:
		info.Summary = *info.FirstPrompt
	}
	return nil
}

var metadataCommand = regexp.MustCompile(`<command-name>(.*?)</command-name>`)
var metadataSkipPrompt = regexp.MustCompile(`^(?:<local-command-stdout>|<session-start-hook>|<tick>|<goal>|\[Request interrupted by user[^\]]*\]|\s*<ide_opened_file>[\s\S]*</ide_opened_file>\s*$|\s*<ide_selection>[\s\S]*</ide_selection>\s*$)`)

func metadataPromptTexts(fields map[string]json.RawMessage) []string {
	for _, key := range []string{"isMeta", "isCompactSummary", "isSidechain"} {
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("true")) {
			return nil
		}
	}
	if v, _ := discoveryString(fields, "teamName"); v != nil && *v != "" {
		return nil
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(fields["message"], &message) != nil {
		return nil
	}
	var text string
	var texts []string
	if json.Unmarshal(message["content"], &text) == nil {
		texts = []string{text}
	} else {
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			return nil
		}
		for _, b := range blocks {
			kind, _ := discoveryString(b, "type")
			if kind == nil {
				continue
			}
			if *kind == "tool_result" {
				return nil
			}
			if *kind == "text" {
				v, _ := discoveryString(b, "text")
				if v != nil {
					texts = append(texts, *v)
				}
			}
		}
	}
	var out []string
	for _, text := range texts {
		text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}
