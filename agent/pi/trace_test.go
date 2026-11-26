package agentpi_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

type submissionTracer struct {
	parent *agent.TraceContext
	starts int
	ends   []error
}

func (s *submissionTracer) StartSpan(_ context.Context, _ string, parent *agent.TraceContext) agent.Span {
	s.starts++
	s.parent = parent
	return s
}

func (s *submissionTracer) TraceContext() agent.TraceContext {
	return agent.TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-b9c7c989f97918e1-01"}
}

func (s *submissionTracer) End(err error) {
	s.ends = append(s.ends, err)
	panic("legacy exporter failed")
}

func TestPiSubmissionTraceIsLocal(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const spanID = "00f067aa0ba902b7"
	parent := agent.TraceContext{
		Traceparent: "00-" + traceID + "-" + spanID + "-01",
		Tracestate:  "vendor=parent",
	}
	for _, test := range []struct {
		name   string
		legacy bool
	}{
		{name: "standard"},
		{name: "legacy", legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			spans := tracetest.NewSpanRecorder()
			reader := sdkmetric.NewManualReader()
			config := agentpi.Config{
				CLIPath:        exe,
				Args:           []string{"-test.run=^TestPiRPCPeer$", "--"},
				Env:            []string{"SDK_PI_PEER=1"},
				TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
				MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
			}
			legacy := &submissionTracer{}
			if test.legacy {
				config.Tracer = legacy
			}
			core := agentpi.New(config)
			capabilities, err := core.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, core.Stop(context.Background())) })
			assert.False(t, capabilities.Supports(agent.CapabilityPromptTrace))
			session, err := core.Open(ctx, agent.OpenOptions{Cwd: t.TempDir()})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, session.Close(context.Background())) })
			assert.False(t, session.Capabilities().Supports(agent.CapabilityPromptTrace))

			submitCtx := agent.WithTraceContext(ctx, parent)
			_, err = session.StartOrSteer(submitCtx, agent.Input{})
			require.Error(t, err)
			assert.Zero(t, legacy.starts)
			receipt, err := session.StartOrSteer(submitCtx, agent.Input{
				Content: []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "hello"}}},
			})
			require.NoError(t, err)
			require.Equal(t, agent.InputAppliedNewWork, receipt.Result)

			var submissions []sdktrace.ReadOnlySpan
			for _, span := range spans.Ended() {
				if span.Name() == agent.SpanStartOrSteer {
					submissions = append(submissions, span)
				}
			}
			if test.legacy {
				assert.Empty(t, submissions)
				assert.Equal(t, 1, legacy.starts)
				assert.Equal(t, &parent, legacy.parent)
				assert.Equal(t, []error{nil}, legacy.ends)
			} else {
				require.Len(t, submissions, 2)
				assert.Contains(t, submissions[0].Attributes(), attribute.String("outcome", "rejected"))
				assert.Contains(t, submissions[1].Attributes(), attribute.String("outcome", string(receipt.Result)))
				for _, span := range submissions {
					assert.Equal(t, traceID, span.Parent().TraceID().String())
					assert.Equal(t, spanID, span.Parent().SpanID().String())
					assert.Equal(t, parent.Tracestate, span.Parent().TraceState().String())
				}
			}

			var data metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(ctx, &data))
			require.Len(t, data.ScopeMetrics, 1)
			counts := make(map[string]uint64)
			for _, m := range data.ScopeMetrics[0].Metrics {
				if m.Name != "agent.sdk.operation.duration" {
					continue
				}
				for _, point := range m.Data.(metricdata.Histogram[float64]).DataPoints {
					operation, _ := point.Attributes.Value("operation")
					if operation.AsString() == "start_or_steer" {
						outcome, _ := point.Attributes.Value("outcome")
						counts[outcome.AsString()] = point.Count
					}
				}
			}
			assert.Equal(t, map[string]uint64{"rejected": 1, string(receipt.Result): 1}, counts)
		})
	}
}
