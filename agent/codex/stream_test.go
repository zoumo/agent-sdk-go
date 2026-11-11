package agentcodex

import (
	"context"

	"github.com/zoumo/agent-sdk-go/codex"
)

// NewWithStream constructs a provider backed by caller-supplied in-process
// streams. It exists only in test builds and never starts a native process.
func NewWithStream(config Config, transport codex.StreamTransport, started func(*codex.Client)) *Core {
	provider := New(config)
	provider.streams = func(context.Context) (streamStart, error) {
		return streamStart{Transport: transport, Started: started}, nil
	}
	return provider
}
