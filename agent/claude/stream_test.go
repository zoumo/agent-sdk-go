package agentclaude

import (
	"context"

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
func NewWithStream(config Config, next func(context.Context) (TestStreamOpen, error)) *Provider {
	provider := New(config)
	provider.streams = func(ctx context.Context) (streamOpen, error) {
		opened, err := next(ctx)
		return streamOpen(opened), err
	}
	return provider
}
