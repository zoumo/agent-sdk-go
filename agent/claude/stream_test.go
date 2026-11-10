package agentclaude

import (
	"context"
	"strings"

	"github.com/zoumo/agent-sdk-go/claude"
)

// TestStreamOpen supplies one in-process native session to a provider. It
// exists only in test builds and never resolves or starts a CLI.
type TestStreamOpen struct {
	Transport claude.StreamTransport
	Workspace string
	Env       []string
	Started   func(*claude.Session)
}

// NewWithStream constructs a provider whose sessions use caller-supplied
// in-process streams.
func NewWithStream(config Config, next func(context.Context, claude.SessionOptions) (TestStreamOpen, error)) *Provider {
	provider := New(config)
	provider.streams = func(ctx context.Context, options claude.SessionOptions) (streamOpen, error) {
		opened, err := next(ctx, options)
		return streamOpen(opened), err
	}
	return provider
}

// TestifyNativeArgs declares the reviewed stdio startup subset accepted by the
// pinned bridge. It is intentionally not a claim of full CLI argv equivalence:
// external MCP declarations, --disallowedTools, --effort, and appended system
// prompts are unsupported by that pin.
// The complete options still go to OpenStreams and its native behavior. Leave
// all other arguments intact so unknown declarations fail closed upstream.
func TestifyNativeArgs(options claude.SessionOptions) ([]string, error) {
	args, err := claude.NativeArgs(options)
	if err != nil {
		return nil, err
	}
	declaration := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--disallowedTools=") &&
			!strings.HasPrefix(arg, "--mcp-config=") &&
			!strings.HasPrefix(arg, "--effort=") &&
			!strings.HasPrefix(arg, "--append-system-prompt=") {
			declaration = append(declaration, arg)
		}
	}
	return declaration, nil
}
