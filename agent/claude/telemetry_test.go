package agentclaude_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
)

func TestLifecycleTelemetry(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, parent := traces.Tracer("host").Start(ctx, "request")
	defer parent.End()

	cwd := t.TempDir()
	configDir := newResumeConfigDir(t)
	transcript := writeResumeTranscript(t, configDir, resumeProjectKey(t, cwd), claudeResumePeerSessionID, cwd)
	peer := newClaudeResumePeer(t, claudeResumePeerScenario{CWD: cwd, TranscriptPath: transcript})
	provider := agentclaude.New(agentclaude.Config{
		CLIPath: peer.CLIPath, Env: peer.Env, ConfigDir: configDir,
		TracerProvider: traces, MeterProvider: meters,
		InitializeTimeout: 3 * time.Second,
	})
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	_, err = provider.Start(ctx)
	require.NoError(t, err)
	options := agent.OpenOptions{Cwd: cwd}
	// Exercise the shared permission entry point directly as well.
	_, err = provider.OpenWithPermissions(ctx, agent.OpenOptions{}, agentclaude.Permissions{})
	require.Error(t, err)
	session, err := provider.Open(ctx, options)
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.Error(t, provider.Stop(ctx))
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.Close(ctx))
	resumed, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: claudeResumePeerSessionID, OpenOptions: options})
	require.NoError(t, err)
	closeSessionOnCleanup(t, resumed)
	require.NoError(t, resumed.Close(ctx))
	require.NoError(t, provider.Stop(ctx))
	require.NoError(t, provider.Stop(ctx))

	want := []struct{ operation, outcome string }{
		{"core.start", "success"},
		{"core.start", "reused"},
		{"core.open", "rejected"},
		{"core.open", "success"},
		{"core.stop", "rejected"},
		{"session.close", "success"},
		{"session.close", "reused"},
		{"core.resume", "success"},
		{"session.close", "success"},
		{"core.stop", "success"},
		{"core.stop", "reused"},
	}
	spans := recorder.Ended()
	require.Len(t, spans, len(want))
	counts := make(map[[2]string]uint64)
	for index, span := range spans {
		result := want[index]
		assert.Equal(t, "agent."+result.operation, span.Name())
		assert.Equal(t, parent.SpanContext(), span.Parent())
		assert.Contains(t, span.Attributes(), attribute.String("provider", "claude"))
		assert.Contains(t, span.Attributes(), attribute.String("outcome", result.outcome))
		counts[[2]string{result.operation, result.outcome}]++
	}
	assertDurationCounts(t, ctx, reader, counts, "")
}

func assertDurationCounts(
	t *testing.T,
	ctx context.Context,
	reader *sdkmetric.ManualReader,
	want map[[2]string]uint64,
	operationFilter string,
) {
	t.Helper()
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	duration := data.ScopeMetrics[0].Metrics[0]
	assert.Equal(t, "agent.sdk.operation.duration", duration.Name)
	assert.Equal(t, "s", duration.Unit)
	points := duration.Data.(metricdata.Histogram[float64]).DataPoints
	got := make(map[[2]string]uint64)
	for _, point := range points {
		assert.Len(t, point.Attributes.ToSlice(), 3)
		provider, _ := point.Attributes.Value("provider")
		operation, _ := point.Attributes.Value("operation")
		outcome, _ := point.Attributes.Value("outcome")
		assert.Equal(t, "claude", provider.AsString())
		key := [2]string{operation.AsString(), outcome.AsString()}
		if operationFilter == "" || operationFilter == operation.AsString() {
			got[key] = point.Count
		}
		assert.Positive(t, point.Sum)
	}
	assert.Equal(t, want, got)
}

func TestInputControlTelemetry(t *testing.T) {
	recorded := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(recorded),
		sdktrace.WithIDGenerator(&telemetryIDs{}),
	)
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	host := telemetryRemoteSpan("11111111111111111111111111111111", "")
	explicit := telemetryRemoteSpan("22222222222222222222222222222222", "vendor=parent")
	ctx = trace.ContextWithRemoteSpanContext(ctx, host)
	explicitCtx := agent.WithTraceContext(ctx, agent.TraceContext{
		Traceparent: "00-" + explicit.TraceID().String() + "-" + explicit.SpanID().String() + "-01",
		Tracestate:  "vendor=parent",
	})

	cwd := t.TempDir()
	frames := append([]string(nil), mockClaudeInit...)
	frames = append(frames, claudeSessionInitFrame(t, "native-telemetry", cwd), claudeInjectFrame(t, "context"))
	frames = append(frames, claudePromptTraceFrames(t, "go_1", agent.TraceContext{
		Traceparent: fmt.Sprintf("00-%s-%016x-01", explicit.TraceID(), 6), Tracestate: "vendor=parent",
	}, false)...)
	frames = append(frames, claudeSubmissionFrame(t, 1, "first"))
	frames = append(frames, claudePromptTraceFrames(t, "go_2", agent.TraceContext{
		Traceparent: fmt.Sprintf("00-%s-%016x-01", host.TraceID(), 7),
	}, false)...)
	frames = append(frames,
		claudeSubmissionFrame(t, 2, "second"),
		claudeStateFrame("native-telemetry", "running", "2026-11-07T14:15:00Z"),
		`>{"type":"control_request","request_id":"go_3","request":{"subtype":"interrupt"}}`,
		`<{"type":"control_response","response":{"subtype":"success","request_id":"go_3","response":{}}}`,
		claudePermissionRequestFrame("permission-telemetry", "Read", "read-tool", `{"file_path":"a.txt"}`),
		`>{"type":"control_response","response":{"subtype":"success","request_id":"permission-telemetry","response":{"behavior":"allow","updatedInput":{"file_path":"a.txt"}}}}`,
		`>{"type":"control_request","request_id":"go_4","request":{"subtype":"interrupt"}}`,
		`!`,
	)
	provider := newTestClaudeProviderWithConfig(t, agentclaude.Config{
		TracerProvider: traces, MeterProvider: meters,
	}, prepareClaudePeer(t, "failed", frames...))
	t.Cleanup(func() { _ = provider.Stop(context.Background()) })
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	stream, err := session.Events()
	require.NoError(t, err)
	require.Eventually(t, func() bool { return agentclaude.InjectSupportedForTest(session) }, time.Second, time.Millisecond)

	_, err = session.StartOrSteer(ctx, agent.Input{})
	require.Error(t, err)
	_, err = session.(agent.Injector).Inject(ctx, claudeTextInput("context"))
	require.NoError(t, err)
	noWork, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	require.Equal(t, agent.InterruptNoActiveWork, noWork.Result)
	for _, input := range []struct {
		ctx  context.Context
		text string
	}{{explicitCtx, "first"}, {ctx, "second"}} {
		result, err := session.StartOrSteer(input.ctx, claudeTextInput(input.text))
		require.NoError(t, err)
		require.Equal(t, agent.InputAppliedBestEffort, result.Result)
	}
	require.Equal(t, agent.StateRunning, nextStateValue(t, ctx, stream))
	interrupt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	require.Equal(t, agent.InterruptAcknowledged, interrupt.Result)
	require.Equal(t, agent.StateRequiresAction, nextStateValue(t, ctx, stream))
	request := nextClaudeInteractionRequested(t, ctx, stream)
	results := make(chan error, 2)
	var answers sync.WaitGroup
	for range 2 {
		answers.Go(func() {
			_, answerErr := session.Answer(ctx, request.Interaction, agent.PermissionAnswer{
				Outcome: agent.PermissionSelected, OptionID: "allow-once",
			})
			results <- answerErr
		})
	}
	answers.Wait()
	close(results)
	var succeeded int
	for answerErr := range results {
		if answerErr == nil {
			succeeded++
		}
	}
	require.Equal(t, 1, succeeded)
	_, err = session.Interrupt(ctx, agent.InterruptOptions{})
	require.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	select {
	case <-session.Done():
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	require.NoError(t, provider.Stop(ctx))

	want := map[[2]string]uint64{
		{"start_or_steer", "rejected"}:                1,
		{"start_or_steer", "applied_best_effort"}:     2,
		{"session.inject", "sent"}:                    1,
		{"session.interrupt", "no_active_work"}:       1,
		{"session.interrupt", "control_acknowledged"}: 1,
		{"session.interrupt", "outcome_unknown"}:      1,
		{"session.answer", "sent"}:                    1,
		{"session.answer", "rejected"}:                1,
		{"core.start", "success"}:                     1,
		{"core.open", "success"}:                      1,
		{"core.stop", "success"}:                      1,
	}
	counts := make(map[[2]string]uint64)
	for _, span := range recorded.Ended() {
		attrs := attribute.NewSet(span.Attributes()...)
		op, _ := attrs.Value("operation")
		outcome, _ := attrs.Value("outcome")
		assert.Equal(t, "agent."+op.AsString(), span.Name())
		for _, attr := range span.Attributes() {
			assert.Contains(t, []attribute.Key{"provider", "operation", "outcome", "error.type"}, attr.Key)
		}
		assert.Empty(t, span.Events())
		counts[[2]string{op.AsString(), outcome.AsString()}]++
		if span.SpanContext().SpanID().String() == "0000000000000006" {
			assert.Equal(t, explicit, span.Parent())
			assert.Equal(t, "vendor=parent", span.SpanContext().TraceState().String())
		} else {
			assert.Equal(t, host, span.Parent())
		}
	}
	assert.Equal(t, want, counts)
	assertDurationCounts(t, ctx, reader, want, "")
}

type telemetryIDs struct{ next atomic.Uint64 }

func (i *telemetryIDs) NewSpanID(context.Context, trace.TraceID) (spanID trace.SpanID) {
	binary.BigEndian.PutUint64(spanID[:], i.next.Add(1))
	return spanID
}

func (i *telemetryIDs) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	id := trace.TraceID{1}
	return id, i.NewSpanID(ctx, id)
}

func telemetryRemoteSpan(hex, tracestate string) trace.SpanContext {
	traceID, _ := trace.TraceIDFromHex(hex)
	state, _ := trace.ParseTraceState(tracestate)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: trace.SpanID{0xaa}, TraceFlags: trace.FlagsSampled, TraceState: state, Remote: true,
	})
}
