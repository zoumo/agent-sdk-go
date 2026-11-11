// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"context"
	"errors"
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

// applyPromptTrace runs with the settings gate held through the following user write.
// Unknown outcomes deliberately force a complete rewrite on the next prompt.
func (s *Session) applyPromptTrace(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return nil
	}
	target := s.traceBaseline
	if trace, ok := TraceContextFromContext(ctx); ok && trace.Validate() == nil {
		target = trace
	}
	if s.traceKnown && target == s.traceCurrent {
		return nil
	}
	attempt, cancel := context.WithTimeout(ctx, promptTraceSettingsTimeout)
	defer cancel()
	_, err := s.client.controlRequest(attempt, "apply_flag_settings", map[string]any{
		"subtype": "apply_flag_settings",
		"settings": map[string]any{
			"env": map[string]string{
				"TRACEPARENT": target.Traceparent,
				"TRACESTATE":  target.Tracestate,
			},
		},
	}, false)
	s.traceKnown = err == nil
	if s.traceKnown {
		s.traceCurrent = target
	}
	var transportError *Error
	if errors.As(err, &transportError) && transportError.Op == "write" {
		return err
	}
	select {
	case <-s.client.done:
		return s.client.operationError()
	default:
		return nil
	}
}
