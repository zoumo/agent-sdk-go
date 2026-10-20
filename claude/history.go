package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/zoumo/agent-sdk-go/internal/history"
)

// HistoryOptions selects exactly one native local main-session transcript.
// ConfigDir is explicit: it is the native CLAUDE_CONFIG_DIR, not a search root.
type HistoryOptions struct {
	SessionID string
	CWD       string
	ConfigDir string
	// Zero selects defaults: 512 MiB source, 100000 records, 8 MiB per line.
	MaxBytes       int64
	MaxRecords     int
	MaxRecordBytes int
}

// HistoryRecord is a retained transcript entry, not a live Message. Message and
// Raw preserve native blocks/unknown fields without manufacturing live events.
type HistoryRecord struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	CWD        string          `json:"cwd"`
	Timestamp  string          `json:"timestamp"`
	Message    json.RawMessage `json:"message"`
	Raw        json.RawMessage `json:"-"`
}

type History = history.Stream[HistoryRecord]

// HistoryError retains the offending entry when a transcript cannot be loaded.
// Raw can contain private transcript content; do not log it without redaction.
type HistoryError struct {
	Reason string
	Raw    json.RawMessage
}

func (e *HistoryError) Error() string { return "claude history: " + e.Reason }

var historyUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var projectUnsafe = regexp.MustCompile(`[^a-zA-Z0-9]`)

func prepareHistory(ctx context.Context, o HistoryOptions) (HistoryOptions, string, error) {
	if ctx == nil {
		return o, "", errors.New("claude history: nil context")
	}
	if err := ctx.Err(); err != nil {
		return o, "", err
	}
	if !historyUUID.MatchString(o.SessionID) || !filepath.IsAbs(o.CWD) || !filepath.IsAbs(o.ConfigDir) {
		return o, "", errors.New("claude history: native UUID and absolute cwd/config directory required")
	}
	if o.MaxBytes < 0 || o.MaxRecords < 0 || o.MaxRecordBytes < 0 {
		return o, "", errors.New("claude history: negative limit")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 512 << 20
	}
	if o.MaxRecords == 0 {
		o.MaxRecords = 100000
	}
	if o.MaxRecordBytes == 0 {
		o.MaxRecordBytes = 8 << 20
	}
	cwd, err := filepath.EvalSymlinks(o.CWD)
	if err != nil {
		return o, "", err
	}
	o.CWD = cwd
	key := projectUnsafe.ReplaceAllString(cwd, "-")
	// Do not prefix-search ambiguous truncated/native legacy project keys.
	if len(key) > 200 {
		return o, "", errors.New("claude history: truncated project keys unsupported")
	}
	path := filepath.Join(o.ConfigDir, "projects", key, o.SessionID+".jsonl")
	return o, path, nil
}

type transcriptLink struct {
	record     HistoryRecord
	offset     int64
	size       int
	digest     [32]byte
	side, meta bool
	team       string
}

func replayTranscript(ctx context.Context, o HistoryOptions, path string, send func(HistoryRecord) error, before func(context.Context) error) error {
	f, err := openHistoryFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	initial, err := f.Stat()
	if err != nil {
		return err
	}
	if !initial.Mode().IsRegular() || initial.Size() > o.MaxBytes {
		return errors.New("claude history: source is not regular or exceeds byte limit")
	}
	links := make(map[string]transcriptLink)
	order := make([]string, 0)
	scan := transcriptScanner(io.NewSectionReader(f, 0, initial.Size()), o.MaxRecordBytes)
	prefixDigest := sha256.New()
	var offset int64
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw := scan.Bytes()
		_, _ = prefixDigest.Write(raw)
		_, _ = prefixDigest.Write([]byte{'\n'})
		size := len(raw)
		start := offset
		offset += int64(size) + 1
		var entry struct {
			HistoryRecord
			IsSidechain bool   `json:"isSidechain"`
			IsMeta      bool   `json:"isMeta"`
			TeamName    string `json:"teamName"`
		}
		bad := func(reason string) error {
			return &HistoryError{Reason: reason, Raw: append(json.RawMessage(nil), raw...)}
		}
		if err := validateTranscriptJSON(raw); err != nil {
			return bad("malformed entry: " + err.Error())
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return bad("malformed entry")
		}
		if entry.Type == "" {
			return bad("missing entry type")
		}
		switch entry.Type {
		case "user", "assistant", "system", "progress", "attachment":
			if entry.UUID == "" {
				return bad("missing chain UUID")
			}
		default:
			if transcriptMetadata(entry.Type) {
				continue
			}
			return bad("unsupported entry type: " + entry.Type)
		}
		if entry.SessionID != o.SessionID || entry.CWD != o.CWD {
			return bad("session/cwd identity mismatch")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return bad("invalid object")
		}
		if _, ok := fields["parentUuid"]; !ok {
			return bad("missing parentUuid evidence")
		}
		if entry.Type == "user" || entry.Type == "assistant" {
			var message map[string]json.RawMessage
			if json.Unmarshal(entry.Message, &message) != nil || message == nil {
				return bad("missing or invalid message")
			}
			content := bytes.TrimSpace(message["content"])
			if len(content) == 0 || (content[0] != '"' && content[0] != '[') {
				return bad("missing or invalid message content")
			}
		}
		if _, ok := links[entry.UUID]; ok {
			return bad("duplicate chain UUID")
		}
		if len(links) >= o.MaxRecords {
			return bad("record limit exceeded")
		}
		// Retain only indexing metadata; payload is read again under backpressure.
		indexRecord := HistoryRecord{Type: entry.Type, UUID: entry.UUID, ParentUUID: entry.ParentUUID}
		links[entry.UUID] = transcriptLink{record: indexRecord, offset: start, size: size, digest: sha256.Sum256(raw), side: entry.IsSidechain, meta: entry.IsMeta, team: entry.TeamName}
		order = append(order, entry.UUID)
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("claude history: scan: %w", err)
	}
	graphError := func(reason, id string) error {
		l := links[id]
		raw := make([]byte, l.size)
		if _, err := f.ReadAt(raw, l.offset); err != nil {
			return err
		}
		return &HistoryError{Reason: reason, Raw: raw}
	}
	// Validate the entire indexed graph, including abandoned retained branches.
	parents := make(map[string]bool)
	checked := make(map[string]bool)
	for _, start := range order {
		path := make([]string, 0)
		visiting := make(map[string]bool)
		for id := start; id != "" && !checked[id]; {
			if err := ctx.Err(); err != nil {
				return err
			}
			if visiting[id] {
				return graphError("parent cycle", id)
			}
			visiting[id] = true
			l := links[id]
			parent := l.record.ParentUUID
			if parent != "" {
				if _, ok := links[parent]; !ok {
					return graphError("missing parent UUID", id)
				}
				parents[parent] = true
			}
			path = append(path, id)
			id = parent
		}
		for _, id := range path {
			checked[id] = true
		}
	}
	// Match the native reader: each terminal contributes its nearest message;
	// choose the latest main candidate, not a main ancestor of a sidechain leaf.
	leaf := ""
	positions := make(map[string]int, len(order))
	for i, id := range order {
		positions[id] = i
	}
	nearest := make(map[string]string)
	for _, terminal := range order {
		if parents[terminal] {
			continue
		}
		id := terminal
		path := make([]string, 0)
		for id != "" {
			if err := ctx.Err(); err != nil {
				return err
			}
			if cached, ok := nearest[id]; ok {
				id = cached
				break
			}
			l := links[id]
			if l.record.Type == "user" || l.record.Type == "assistant" {
				break
			}
			path = append(path, id)
			id = l.record.ParentUUID
		}
		for _, p := range path {
			nearest[p] = id
		}
		if id == "" {
			continue
		}
		l := links[id]
		if !l.side && !l.meta && l.team == "" && (leaf == "" || positions[id] > positions[leaf]) {
			leaf = id
		}
	}
	if leaf == "" {
		return errors.New("claude history: no main conversation messages")
	}
	chain := make([]string, 0)
	seen := make(map[string]bool)
	for id := leaf; id != ""; {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[id] {
			return graphError("parent cycle", id)
		}
		seen[id] = true
		l, ok := links[id]
		if !ok {
			return &HistoryError{Reason: "missing parent UUID"}
		}
		if l.side || l.team != "" {
			return graphError("main chain crosses sidechain", id)
		}
		chain = append(chain, id)
		id = l.record.ParentUUID
	}
	if err := before(ctx); err != nil {
		return err
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		l := links[chain[i]]
		raw := make([]byte, l.size)
		if _, err := f.ReadAt(raw, l.offset); err != nil {
			return err
		}
		if sha256.Sum256(raw) != l.digest {
			return &HistoryError{Reason: "entry changed during replay", Raw: raw}
		}
		var r HistoryRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			return &HistoryError{Reason: "entry changed during replay", Raw: raw}
		}
		if r.UUID != l.record.UUID || r.ParentUUID != l.record.ParentUUID || r.CWD != o.CWD || r.SessionID != o.SessionID {
			return &HistoryError{Reason: "entry identity changed during replay", Raw: raw}
		}
		r.Raw = raw
		if err := send(r); err != nil {
			return err
		}
	}
	var frozen [32]byte
	copy(frozen[:], prefixDigest.Sum(nil))
	return verifyTranscriptSnapshot(ctx, f, path, initial, frozen, o)
}
