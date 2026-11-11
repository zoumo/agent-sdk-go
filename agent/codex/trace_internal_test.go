package agentcodex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/agent"
)

type admissionTraceFunc func(context.Context, string, *agent.TraceContext) agent.Span

func (f admissionTraceFunc) StartSpan(ctx context.Context, name string, parent *agent.TraceContext) agent.Span {
	return f(ctx, name, parent)
}

func TestTraceRejectedLocalSubmissionDoesNotStartSpan(t *testing.T) {
	for _, name := range []string{"invalid", "closed", "concurrent", "unknown"} {
		t.Run(name, func(t *testing.T) {
			starts := 0
			tracer := admissionTraceFunc(func(context.Context, string, *agent.TraceContext) agent.Span {
				starts++
				return nil
			})
			s := &session{provider: New(Config{Tracer: tracer})}
			input := agent.Input{Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}}}
			switch name {
			case "invalid":
				input = agent.Input{}
			case "closed":
				s.closed = true
			case "concurrent":
				s.submitting = true
			case "unknown":
				s.submitUnknown = true
			}
			receipt, err := s.StartOrSteer(context.Background(), input)
			if name == "invalid" || name == "closed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, agent.InputNotApplied, receipt.Result)
				require.NotNil(t, receipt.Acceptance.Sent)
				assert.False(t, *receipt.Acceptance.Sent)
			}
			assert.Zero(t, starts)
		})
	}
}
