//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package claude_test

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestChildQueryRejectsFIFO(t *testing.T) {
	scope, root := childStore(t)
	putChild(t, root, "agent-x.jsonl", "")
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "agent-pipe.jsonl"), 0600))
	p, e := claude.GetSubagentMessages(context.Background(), childParent, "x", claude.SubagentMessagesOptions{Scope: scope})
	childError(t, p, e, "unsupported")
}
