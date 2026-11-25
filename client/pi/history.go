package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// ErrAmbiguousSession indicates that more than one saved file in scope has
// the same native session ID.
var ErrAmbiguousSession = errors.New("pi: ambiguous saved session ID")

// ErrInvalidHistory indicates corruption in the selected saved session.
var ErrInvalidHistory = errors.New("pi: invalid saved session history")

// HistoryScope selects one explicit session directory and workspace. Both
// paths must be absolute. Only immediate regular .jsonl files are considered.
type HistoryScope struct {
	Cwd        string
	SessionDir string
}

// SessionInfo contains metadata from a saved Pi session header. Version is 1
// when the native legacy header omitted it. Raw preserves the original header.
type SessionInfo struct {
	ID            string          `json:"id"`
	Cwd           string          `json:"cwd"`
	Version       int             `json:"version"`
	Timestamp     string          `json:"timestamp"`
	ParentSession string          `json:"parentSession"`
	SessionFile   string          `json:"-"`
	Raw           json.RawMessage `json:"-"`
}

// HistoryWindow selects a range of records by their position in the local
// file. Offset is zero-based and Limit zero reads to the end of the file.
// The header, blank lines and a partial trailing write do not count.
type HistoryWindow struct {
	Offset, Limit int
}

// HistoryEntry preserves one saved entry and its native branch links. Legacy
// entries can omit ID and ParentID. Raw includes all fields and distinguishes
// an explicit null parent from a missing parent.
type HistoryEntry struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	ParentID *string         `json:"parentId"`
	Raw      json.RawMessage `json:"-"`
}

// SessionHistory contains entries in original file order, including branches.
// With HasMore, NextOffset can continue reading this file from disk. Queries
// are independent reads and do not provide a snapshot across calls. A malformed
// final record without a newline sets TrailingPartial and is excluded.
type SessionHistory struct {
	Info            SessionInfo
	Entries         []HistoryEntry
	NextOffset      int
	HasMore         bool
	TrailingPartial bool
}

// ListSessions discovers sessions by their saved header ID, ordered by filename.
// It skips files without a valid header and files outside the requested workspace.
func ListSessions(ctx context.Context, scope HistoryScope) ([]SessionInfo, error) {
	root, err := openHistoryScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return listSavedSessions(ctx, root, scope.Cwd, "")
}

// GetSessionInfo reads header metadata for an exact saved session ID.
func GetSessionInfo(ctx context.Context, scope HistoryScope, sessionID string) (SessionInfo, error) {
	root, err := openHistoryScope(ctx, scope)
	if err != nil {
		return SessionInfo{}, err
	}
	defer root.Close()
	return findSavedSession(ctx, root, scope.Cwd, sessionID)
}

// ReadSessionHistory reads saved entries without launching Pi or following
// ParentSession. Its offset is independent of the live get_entries cursor.
func ReadSessionHistory(ctx context.Context, scope HistoryScope, sessionID string, window HistoryWindow) (SessionHistory, error) {
	if window.Offset < 0 || window.Limit < 0 {
		return SessionHistory{}, errors.New("pi: negative saved history offset or limit")
	}
	root, err := openHistoryScope(ctx, scope)
	if err != nil {
		return SessionHistory{}, err
	}
	defer root.Close()
	info, err := findSavedSession(ctx, root, scope.Cwd, sessionID)
	if err != nil {
		return SessionHistory{}, err
	}

	file, size, err := openRegularHistoryFile(root, filepath.Base(info.SessionFile))
	if err != nil {
		return SessionHistory{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.NewSectionReader(file, 0, size))
	scanner.Buffer(make([]byte, 4096), 64<<20)
	scanner.Split(scanSavedLine)
	if !scanner.Scan() || !bytes.Equal(savedLine(scanner.Bytes()), info.Raw) {
		return SessionHistory{}, fmt.Errorf("%w: header changed during lookup", ErrInvalidHistory)
	}
	return readSavedEntries(ctx, scanner, info, window)
}

func openHistoryScope(ctx context.Context, scope HistoryScope) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(scope.Cwd) || !filepath.IsAbs(scope.SessionDir) {
		return nil, errors.New("pi: saved history requires absolute Cwd and SessionDir")
	}
	return os.OpenRoot(scope.SessionDir)
}

func listSavedSessions(ctx context.Context, root *os.Root, cwd, sessionID string) ([]SessionInfo, error) {
	files, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	sessions := []SessionInfo{}
	seen := make(map[string]bool)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !file.Type().IsRegular() || filepath.Ext(file.Name()) != ".jsonl" {
			continue
		}
		info, err := readSavedSessionInfo(root, file.Name())
		if errors.Is(err, errInvalidHeader) || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if (sessionID != "" && info.ID != sessionID) || checkSessionCwd(cwd, info.Cwd) != nil {
			continue
		}
		if seen[info.ID] {
			return nil, fmt.Errorf("%w %q", ErrAmbiguousSession, info.ID)
		}
		seen[info.ID] = true
		sessions = append(sessions, info)
	}
	return sessions, nil
}

func findSavedSession(ctx context.Context, root *os.Root, cwd, sessionID string) (SessionInfo, error) {
	if sessionID == "" {
		return SessionInfo{}, errors.New("pi: an exact saved session ID is required")
	}
	sessions, err := listSavedSessions(ctx, root, cwd, sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	if len(sessions) == 0 {
		return SessionInfo{}, fmt.Errorf("pi: saved session %q: %w", sessionID, fs.ErrNotExist)
	}
	return sessions[0], nil
}

func readSavedSessionInfo(root *os.Root, name string) (SessionInfo, error) {
	file, _, err := openRegularHistoryFile(root, name)
	if err != nil {
		return SessionInfo{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxSavedHeaderBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
			return SessionInfo{}, err
		}
		return SessionInfo{}, errInvalidHeader
	}
	if _, err := parseSessionHeader(scanner.Bytes()); err != nil {
		return SessionInfo{}, err
	}
	var info SessionInfo
	if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
		return SessionInfo{}, errInvalidHeader
	}
	if info.Version == 0 {
		info.Version = 1
	}
	info.SessionFile = filepath.Join(root.Name(), name)
	info.Raw = slices.Clone(scanner.Bytes())
	return info, nil
}

func openRegularHistoryFile(root *os.Root, name string) (*os.File, int64, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errInvalidHeader
	}
	file, err := openSavedSessionFile(root, name)
	if err != nil {
		return nil, 0, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, 0, errInvalidHeader
	}
	return file, opened.Size(), nil
}

func readSavedEntries(ctx context.Context, scanner *bufio.Scanner, info SessionInfo, window HistoryWindow) (SessionHistory, error) {
	history := SessionHistory{Info: info, NextOffset: window.Offset, Entries: []HistoryEntry{}}
	offset := 0
	for lineNumber := 2; scanner.Scan(); lineNumber++ {
		if err := ctx.Err(); err != nil {
			return SessionHistory{}, err
		}
		line := savedLine(scanner.Bytes())
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry HistoryEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.Type == "" || entry.Type == "session" {
			if !bytes.HasSuffix(scanner.Bytes(), []byte{'\n'}) && incompleteSavedJSON(line) {
				history.TrailingPartial = true
				break
			}
			return SessionHistory{}, fmt.Errorf("%w: %s line %d", ErrInvalidHistory, filepath.Base(info.SessionFile), lineNumber)
		}
		offset++
		if offset <= window.Offset {
			continue
		}
		if window.Limit > 0 && len(history.Entries) == window.Limit {
			history.HasMore = true
			continue
		}
		entry.Raw = slices.Clone(line)
		history.Entries = append(history.Entries, entry)
		history.NextOffset = offset
	}
	if err := scanner.Err(); err != nil {
		return SessionHistory{}, fmt.Errorf("%w: %w", ErrInvalidHistory, err)
	}
	return history, ctx.Err()
}

// Keep the line ending so a malformed complete line can be distinguished
// from an interrupted final append.
func scanSavedLine(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func savedLine(line []byte) []byte {
	return bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
}

func incompleteSavedJSON(line []byte) bool {
	var raw json.RawMessage
	return errors.Is(json.NewDecoder(bytes.NewReader(line)).Decode(&raw), io.ErrUnexpectedEOF)
}
