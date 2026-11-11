package codex

import (
	"context"
	"strings"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// TraceContext carries one native prompt's W3C request-envelope metadata.
// It is independent of the optional unified agent package.
type TraceContext = types.W3cTraceContext

type traceContextKey struct{}

// WithTraceContext attaches a carrier without validating it. Only Session.Submit
// consumes it; raw Client calls and thread operations remain trace-free.
func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceContextFromContext reads the current carrier without validating it.
// Derived contexts inherit the value; repeated writes keep the latest value.
func TraceContextFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}

// Snapshot only known, valid W3C fields. Native optional null/absent state and
// empty tracestate all mean no tracestate on this request.
func submissionTrace(ctx context.Context) *types.W3cTraceContext {
	trace, ok := TraceContextFromContext(ctx)
	if !ok {
		return nil
	}
	parent, ok := trace.Traceparent.Get()
	state := trace.Tracestate.ValueOr("")
	if !ok || !validTraceContext(parent, state) {
		return nil
	}
	snapshot := types.W3cTraceContext{Traceparent: types.Some(parent)}
	if state != "" {
		snapshot.Tracestate = types.Some(state)
	}
	return &snapshot
}

func validTraceContext(parent, state string) bool {
	if len(parent) < 55 || !traceASCII(parent, false) ||
		parent[2] != '-' || parent[35] != '-' || parent[52] != '-' ||
		!traceHex(parent[:2]) || parent[:2] == "ff" ||
		!traceHex(parent[3:35]) || parent[3:35] == strings.Repeat("0", 32) ||
		!traceHex(parent[36:52]) || parent[36:52] == strings.Repeat("0", 16) ||
		!traceHex(parent[53:55]) {
		return false
	}
	if len(parent) != 55 && (parent[:2] == "00" || parent[55] != '-') {
		return false
	}
	if !validTracestate(state) {
		return false
	}
	return true
}

func traceASCII(value string, whitespace bool) bool {
	for i := range len(value) {
		if whitespace && value[i] == '\t' {
			continue
		}
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func traceHex(value string) bool {
	for i := range len(value) {
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func validTracestate(state string) bool {
	if state == "" {
		return true
	}
	if !traceASCII(state, true) {
		return false
	}
	members := strings.Split(state, ",")
	if len(members) > 32 {
		return false
	}
	keys := make(map[string]bool, len(members))
	for _, member := range members {
		member = strings.Trim(member, " \t")
		// W3C permits empty list members when multiple headers are combined.
		if member == "" {
			continue
		}
		key, value, ok := strings.Cut(member, "=")
		if !ok || !traceKey(key) || keys[key] || len(value) == 0 || len(value) > 256 {
			return false
		}
		for i := range len(value) {
			if value[i] < 0x20 || value[i] > 0x7e || value[i] == '=' {
				return false
			}
		}
		keys[key] = true
	}
	return true
}

func traceKey(key string) bool {
	if tenant, system, multi := strings.Cut(key, "@"); multi {
		return traceKeyPart(tenant, 241, true) && traceKeyPart(system, 14, false)
	}
	return traceKeyPart(key, 256, false)
}

func traceKeyPart(part string, max int, digitStart bool) bool {
	if len(part) == 0 || len(part) > max {
		return false
	}
	alpha := func(c byte) bool { return c >= 'a' && c <= 'z' }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	if !alpha(part[0]) && !(digitStart && digit(part[0])) {
		return false
	}
	for i := range len(part) {
		c := part[i]
		if !alpha(c) && !digit(c) && c != '_' && c != '-' && c != '*' && c != '/' {
			return false
		}
	}
	return true
}
