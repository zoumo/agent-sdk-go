// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"strings"
	"time"
)

// Bound the observability-only response wait independently of user input.
const promptTraceSettingsTimeout = time.Second

func startupTrace(env []string) TraceContext {
	var trace TraceContext
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "TRACEPARENT":
			trace.Traceparent = value
		case "TRACESTATE":
			trace.Tracestate = value
		}
	}
	return trace
}

// applyPromptTrace runs with settingsMu held through the following user write.
// Unknown outcomes deliberately force a complete rewrite on the next prompt.
func (s *Session) applyPromptTrace(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	target := s.traceBaseline
	if trace, ok := TraceContextFromContext(ctx); ok && trace.Validate() == nil {
		target = trace
	}
	if s.traceKnown && target == s.traceCurrent {
		return
	}
	attempt, cancel := context.WithTimeout(ctx, promptTraceSettingsTimeout)
	defer cancel()
	_, err := s.client.controlRequestWithPolicy(attempt, "apply_flag_settings", map[string]any{
		"subtype": "apply_flag_settings",
		"settings": map[string]any{
			"env": map[string]string{
				"TRACEPARENT": target.Traceparent,
				"TRACESTATE":  target.Tracestate,
			},
		},
	}, false, true)
	s.traceKnown = err == nil
	if s.traceKnown {
		s.traceCurrent = target
	}
}
