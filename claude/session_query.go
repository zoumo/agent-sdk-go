package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SessionQueryScope selects only local storage, without a Client or Session.
// ConfigDir must be explicit and absolute. ProjectDir, when supplied, is an
// absolute native project path; no worktree expansion or environment lookup runs.
// Empty ProjectDir searches immediate projects directories, with MaxEntries
// bounding all visited directory entries (default 10000).
type SessionQueryScope struct {
	ConfigDir  string
	ProjectDir string
	MaxEntries int
}

// SessionMessagesOptions bounds a fresh read, not I/O pagination. All limits
// must be nonnegative. Zero selects defaults: Limit=1000, MaxMessages=10000,
// MaxBytes=64 MiB, MaxLines/MaxRecords=100000, MaxLineBytes=8 MiB.
// Offset/Limit apply after native chain reconstruction and visibility filtering.
type SessionMessagesOptions struct {
	Scope                              SessionQueryScope
	Limit, Offset                      int
	MaxMessages                        int
	MaxBytes                           int64
	MaxLines, MaxRecords, MaxLineBytes int
}

// SessionMessage is saved user/assistant content, never a live event or request.
// Message and Raw retain exact JSON numbers and unknown data, owned by caller.
// Optional parent/timestamp fields retain omitted/null/value through Raw.
type SessionMessage struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	SessionID  string          `json:"session_id"`
	ParentUUID *string         `json:"parent_uuid,omitempty"`
	Timestamp  *string         `json:"timestamp,omitempty"`
	Message    json.RawMessage `json:"message"`
	Raw        json.RawMessage `json:"raw"`
}

type SessionMessagesCoverage struct {
	Status                                 string // complete for the selected local source, partial on a read failure
	ConfigDir, ProjectDir, Path, SessionID string
	Entries, Lines, Records                int
	Bytes                                  int64
	// Atomic is always false. Stability checks detect changes observed during this
	// read; separate calls/pages have no snapshot token or atomicity guarantee.
	Atomic bool
	Limits SessionMessagesOptions
}
type SessionMessagesPage struct {
	Messages []SessionMessage
	Coverage SessionMessagesCoverage
}

// SessionQueryError.Kind is invalid-input, missing, inaccessible, corrupt,
// unsupported, ambiguous, resource-limit, or changed. Cause retains OS/context
// errors. Raw may contain private data; redact before logging.
type SessionQueryError struct {
	Kind, Path, Reason string
	Raw                json.RawMessage
	Cause              error
}

func (e *SessionQueryError) Error() string {
	return fmt.Sprintf("claude session query %s: %s: %s", e.Kind, e.Path, e.Reason)
}
func (e *SessionQueryError) Unwrap() error { return e.Cause }
func queryError(kind, path, reason string, err error) error {
	return &SessionQueryError{Kind: kind, Path: path, Reason: reason, Cause: err}
}
func queryAccess(path string, err error) error {
	kind := "inaccessible"
	if errors.Is(err, os.ErrNotExist) {
		kind = "missing"
	}
	return queryError(kind, path, "storage access", err)
}

// GetSessionMessages reads a bounded main-conversation transcript. It does not
// open/resume a Session, launch a process, subscribe, inject events or grant
// answer authority. Any error returns no messages; partial reads never succeed.
func GetSessionMessages(ctx context.Context, sessionID string, o SessionMessagesOptions) (page SessionMessagesPage, err error) {
	page.Messages = []SessionMessage{}
	page.Coverage = SessionMessagesCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, SessionID: sessionID}
	defer func() {
		if err != nil {
			page.Messages = []SessionMessage{}
		}
	}()
	if ctx == nil {
		return page, queryError("invalid-input", "", "nil context", ErrInvalidInput)
	}
	if err = ctx.Err(); err != nil {
		return page, err
	}
	if !historyUUID.MatchString(sessionID) || !filepath.IsAbs(o.Scope.ConfigDir) || (o.Scope.ProjectDir != "" && !filepath.IsAbs(o.Scope.ProjectDir)) {
		return page, queryError("invalid-input", "", "native session UUID and absolute config/project required", ErrInvalidInput)
	}
	if o.Limit < 0 || o.Offset < 0 || o.MaxMessages < 0 || o.MaxBytes < 0 || o.MaxLines < 0 || o.MaxRecords < 0 || o.MaxLineBytes < 0 || o.Scope.MaxEntries < 0 {
		return page, queryError("invalid-input", "", "negative limit/offset", ErrInvalidInput)
	}
	if o.Limit == 0 {
		o.Limit = 1000
	}
	if o.MaxMessages == 0 {
		o.MaxMessages = 10000
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 64 << 20
	}
	if o.MaxLines == 0 {
		o.MaxLines = 100000
	}
	if o.MaxRecords == 0 {
		o.MaxRecords = 100000
	}
	if o.MaxLineBytes == 0 {
		o.MaxLineBytes = 8 << 20
	}
	if o.Scope.MaxEntries == 0 {
		o.Scope.MaxEntries = 10000
	}
	page.Coverage.Limits = o
	if o.Limit > o.MaxMessages {
		return page, queryError("resource-limit", "", "requested return limit exceeds MaxMessages", nil)
	}
	root, projects, key, e := openQueryScope(o.Scope)
	if e != nil {
		return page, e
	}
	defer root.Close()
	defer projects.Close()
	projectRoot, rel, e := locateQuerySource(ctx, projects, key, sessionID+".jsonl", &page.Coverage, o.Scope.MaxEntries)
	if e != nil {
		return page, e
	}
	defer projectRoot.Close()
	page.Coverage.Path = filepath.Join(o.Scope.ConfigDir, "projects", rel)
	page.Coverage.Status = "partial"
	name := filepath.Base(rel)
	st, e := projectRoot.Lstat(name)
	if e != nil {
		return page, queryAccess(page.Coverage.Path, e)
	}
	if !st.Mode().IsRegular() {
		return page, queryError("unsupported", page.Coverage.Path, "not a regular file", ErrUnsupported)
	}
	f, e := openDiscoveryFile(projectRoot, name)
	if e != nil {
		if errors.Is(e, ErrUnsupported) {
			return page, queryError("unsupported", page.Coverage.Path, "local query platform", e)
		}
		return page, queryAccess(page.Coverage.Path, e)
	}
	defer f.Close()
	initial, e := f.Stat()
	if e != nil {
		return page, queryAccess(page.Coverage.Path, e)
	}
	if !os.SameFile(st, initial) || !initial.Mode().IsRegular() {
		return page, queryError("changed", page.Coverage.Path, "file changed during open", nil)
	}
	if initial.Size() > o.MaxBytes {
		return page, queryError("resource-limit", page.Coverage.Path, "source bytes", nil)
	}
	data, e := readQueryBytes(ctx, f, initial.Size())
	if e != nil {
		return page, e
	}
	page.Coverage.Bytes = int64(len(data))
	records, e := parseQueryTranscript(ctx, data, sessionID, o, &page.Coverage)
	if e != nil {
		return page, e
	}
	chain, e := queryConversation(ctx, records, page.Coverage.Path)
	if e != nil {
		return page, e
	}
	visible := 0
	for _, r := range chain {
		if err = ctx.Err(); err != nil {
			return page, err
		}
		if !r.visible() {
			continue
		}
		if visible >= o.Offset && len(page.Messages) < o.Limit {
			page.Messages = append(page.Messages, SessionMessage{Type: r.Type, UUID: r.UUID, SessionID: r.SessionID, ParentUUID: r.parent, Timestamp: r.timestamp, Message: bytes.Clone(r.Message), Raw: bytes.Clone(r.Raw)})
		}
		visible++
	}
	// Verify the entire original prefix and file identity, not only returned rows.
	if e = verifyQuerySource(ctx, f, root, filepath.Join("projects", rel), initial, sha256.Sum256(data)); e != nil {
		return page, e
	}
	page.Coverage.Status = "complete"
	return page, nil
}

func readQueryBytes(ctx context.Context, f *os.File, size int64) ([]byte, error) {
	var b bytes.Buffer
	r := io.NewSectionReader(f, 0, size)
	chunk := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, e := r.Read(chunk)
		b.Write(chunk[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
	}
	if int64(b.Len()) != size {
		return nil, queryError("changed", f.Name(), "source shortened", io.ErrUnexpectedEOF)
	}
	return b.Bytes(), nil
}

// Each directory is pinned separately. In-tree symlinks are rejected, including
// same-root symlinks; os.Root additionally confines component races to the root.
func queryDirectory(root *os.Root, name string) (*os.Root, error) {
	st, e := root.Lstat(name)
	if e != nil {
		return nil, queryAccess(name, e)
	}
	if !st.IsDir() {
		return nil, queryError("unsupported", name, "not an ordinary directory", ErrUnsupported)
	}
	next, e := root.OpenRoot(name)
	if e != nil {
		return nil, queryAccess(name, e)
	}
	opened, e := next.Stat(".")
	if e != nil || !os.SameFile(st, opened) {
		next.Close()
		return nil, queryError("changed", name, "directory changed during open", e)
	}
	return next, nil
}
func locateQuerySource(ctx context.Context, projects *os.Root, key, name string, c *SessionMessagesCoverage, maxEntries int) (*os.Root, string, error) {
	if key != "" {
		r, e := queryDirectory(projects, key)
		return r, filepath.Join(key, name), e
	}
	dir, e := projects.Open(".")
	if e != nil {
		return nil, "", queryAccess("projects", e)
	}
	defer dir.Close()
	var found *os.Root
	var path string
	fail := func(err error) (*os.Root, string, error) {
		if found != nil {
			found.Close()
		}
		return nil, "", err
	}
	for {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		entries, re := dir.ReadDir(1)
		if len(entries) == 0 && re == io.EOF {
			break
		}
		if re != nil && re != io.EOF {
			return fail(queryAccess("projects", re))
		}
		for _, entry := range entries {
			if c.Entries >= maxEntries {
				return fail(queryError("resource-limit", "projects", "discovery entries", nil))
			}
			c.Entries++
			if !entry.IsDir() {
				return fail(queryError("unsupported", entry.Name(), "non-directory project entry", ErrUnsupported))
			}
			r, e := queryDirectory(projects, entry.Name())
			if e != nil {
				return fail(e)
			}
			_, e = r.Lstat(name)
			if errors.Is(e, os.ErrNotExist) {
				r.Close()
				continue
			}
			if e != nil {
				r.Close()
				return fail(queryAccess(entry.Name(), e))
			}
			if found != nil {
				r.Close()
				return fail(queryError("ambiguous", name, "session found in multiple projects", nil))
			}
			found = r
			path = filepath.Join(entry.Name(), name)
		}
	}
	if found == nil {
		return fail(queryError("missing", name, "session not found in declared project scope", os.ErrNotExist))
	}
	return found, path, nil
}

// Verify using both the pinned source handle and the original root-relative
// pathname, so replacement of an ancestor directory is also observable.
func verifyQuerySource(ctx context.Context, f *os.File, root *os.Root, path string, initial os.FileInfo, digest [32]byte) error {
	again, e := readQueryBytes(ctx, f, initial.Size())
	if e != nil {
		return e
	}
	final, e := f.Stat()
	if e != nil {
		return queryAccess(path, e)
	}
	current, e := root.Lstat(path)
	if e != nil {
		return queryError("changed", path, "source removed/replaced", e)
	}
	if !os.SameFile(initial, current) || final.Size() != initial.Size() || current.Size() != initial.Size() || !final.ModTime().Equal(initial.ModTime()) || digest != sha256.Sum256(again) {
		return queryError("changed", path, "source append/rewrite/replacement observed", nil)
	}
	return ctx.Err()
}

// Shared scope resolution for messages, metadata and discovery. No default
// config lookup, worktree expansion or second filename resolution policy.
func openQueryScope(scope SessionQueryScope) (*os.Root, *os.Root, string, error) {
	if !filepath.IsAbs(scope.ConfigDir) || (scope.ProjectDir != "" && !filepath.IsAbs(scope.ProjectDir)) || scope.MaxEntries < 0 {
		return nil, nil, "", queryError("invalid-input", "", "absolute config/project and nonnegative entries required", ErrInvalidInput)
	}
	root, e := os.OpenRoot(scope.ConfigDir)
	if e != nil {
		return nil, nil, "", queryAccess(scope.ConfigDir, e)
	}
	projects, e := queryDirectory(root, "projects")
	if e != nil {
		root.Close()
		return nil, nil, "", e
	}
	key := ""
	if scope.ProjectDir != "" {
		project, err := filepath.EvalSymlinks(scope.ProjectDir)
		if err != nil {
			e = queryAccess(scope.ProjectDir, err)
		} else {
			key = projectUnsafe.ReplaceAllString(project, "-")
			if len(key) > 200 {
				e = queryError("unsupported", project, "truncated project keys", ErrUnsupported)
			}
		}
	}
	if e != nil {
		projects.Close()
		root.Close()
		return nil, nil, "", e
	}
	return root, projects, key, nil
}
