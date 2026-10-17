package codex

import (
	"fmt"
	"time"

	"github.com/zoumo/codex-sdk-go/protocol"
)

// Limits are per connection. Zero values select DefaultLimits. Negative values
// are rejected. Incoming and outgoing frame limits exclude the newline.
type Limits struct {
	MaxFrameBytes   int
	OutboundQueue   int
	PendingCalls    int
	Subscriptions   int
	EventQueue      int
	DiagnosticBytes int
	CallTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxFrameBytes: 8 << 20, OutboundQueue: 32, PendingCalls: 128,
		Subscriptions: 32, EventQueue: 128, DiagnosticBytes: 64 << 10,
		CallTimeout: 30 * time.Second, WriteTimeout: 10 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	}
}
func (l Limits) normalized() (Limits, error) {
	d := DefaultLimits()
	ints := []struct {
		v   *int
		def int
	}{{&l.MaxFrameBytes, d.MaxFrameBytes}, {&l.OutboundQueue, d.OutboundQueue}, {&l.PendingCalls, d.PendingCalls}, {&l.Subscriptions, d.Subscriptions}, {&l.EventQueue, d.EventQueue}, {&l.DiagnosticBytes, d.DiagnosticBytes}}
	for _, x := range ints {
		if *x.v < 0 {
			return l, fmt.Errorf("codex: limits must be nonnegative")
		}
		if *x.v == 0 {
			*x.v = x.def
		}
	}
	ds := []struct {
		v   *time.Duration
		def time.Duration
	}{{&l.CallTimeout, d.CallTimeout}, {&l.WriteTimeout, d.WriteTimeout}, {&l.ShutdownTimeout, d.ShutdownTimeout}}
	for _, x := range ds {
		if *x.v < 0 {
			return l, fmt.Errorf("codex: timeouts must be nonnegative")
		}
		if *x.v == 0 {
			*x.v = x.def
		}
	}
	return l, nil
}

// StdioOptions explicitly selects the child and its initialization. No shell is
// invoked and no executable, credentials, or account is discovered by the SDK.
// Env nil inherits the current environment as os/exec does; a non-nil slice
// replaces it. Choose a clean environment and Codex home when isolation matters.
type StdioOptions struct {
	Executable string
	Args       []string
	Env        []string
	Dir        string
	Initialize protocol.InitializeParams
	Limits     Limits
}

// DiagnosticSnapshot never contains the child's raw stderr or wire payloads.
// Tail is a bounded sequence of redaction markers. Counters saturate at uint64 max.
type DiagnosticSnapshot struct {
	StderrBytes        uint64
	UnmatchedResponses uint64
	Notifications      uint64
	Tail               string
}
