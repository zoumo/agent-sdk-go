package claude

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"regexp"
)

var (
	historyUUID   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	projectUnsafe = regexp.MustCompile(`[^a-zA-Z0-9]`)
)

// transcriptScanner bounds each native JSONL record for saved-history queries.
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

// transcriptMetadata reports known non-conversation records shared by all
// saved-history readers.
func transcriptMetadata(kind string) bool {
	switch kind {
	case "summary", "file-history-snapshot", "queue-operation", "last-prompt", "custom-title", "tag", "agent-name", "agent-color", "pr-link", "saved_hook_context", "atis-latch", "mode":
		return true
	}
	return false
}
