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
)

func transcriptScanner(r io.Reader, limit int) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, min(4096, limit)), limit)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("claude history: incomplete final JSONL record")
		}
		return 0, nil, nil
	})
	return s
}

func transcriptMetadata(kind string) bool {
	switch kind {
	case "summary", "file-history-snapshot", "queue-operation", "last-prompt", "custom-title", "tag", "agent-name", "agent-color", "pr-link", "saved_hook_context", "atis-latch", "mode":
		return true
	}
	return false
}

// Native resume appends configuration metadata (observed CLI 2.1.258: atis-latch
// and mode). Allow only known metadata appended to an unchanged frozen prefix;
// new conversation entries, corrupt/unknown tails and rewrites still fail.
func verifyTranscriptSnapshot(ctx context.Context, f *os.File, path string, initial os.FileInfo, want [32]byte, o HistoryOptions) error {
	final, err := f.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(initial, current) || final.Size() < initial.Size() || final.Size() > o.MaxBytes {
		return &HistoryError{Reason: "transcript changed during replay"}
	}
	digest := sha256.New()
	r := io.NewSectionReader(f, 0, initial.Size())
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := r.Read(buf)
		if n > 0 {
			_, _ = digest.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if !bytes.Equal(digest.Sum(nil), want[:]) {
		return &HistoryError{Reason: "transcript prefix changed during replay"}
	}
	scan := transcriptScanner(io.NewSectionReader(f, initial.Size(), final.Size()-initial.Size()), o.MaxRecordBytes)
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw := scan.Bytes()
		bad := func() error {
			return &HistoryError{Reason: "unsupported or changed transcript tail", Raw: append(json.RawMessage(nil), raw...)}
		}
		if validateTranscriptJSON(raw) != nil {
			return bad()
		}
		var entry struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
		}
		if json.Unmarshal(raw, &entry) != nil || !transcriptMetadata(entry.Type) || (entry.SessionID != "" && entry.SessionID != o.SessionID) || (entry.CWD != "" && entry.CWD != o.CWD) {
			return bad()
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if after.Size() != final.Size() || !after.ModTime().Equal(final.ModTime()) {
		return &HistoryError{Reason: "transcript changed during tail verification"}
	}
	return nil
}
