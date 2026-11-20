package agentcodex_test

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
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
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

	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root")+`}`,
		`>{"id":93,"method":"thread/unsubscribe","params":{"threadId":"root"}}`,
		`<{"id":93,"result":{"status":"unsubscribed"}}`,
		`>{"id":94,"method":"thread/resume","params":{"cwd":"/synthetic","excludeTurns":true,"threadId":"root"}}`,
		`<{"id":94,"result":`+codexThreadResumeResult("root")+`}`,
		`>{"id":95,"method":"thread/unsubscribe","params":{"threadId":"root"}}`,
		`<{"id":95,"result":{"status":"unsubscribed"}}`,
	)
	provider := newTestProviderWithWrappers(t, prepareCodexPeer(t, frames...), codextypes.InitializeParams{
		ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
	}, testStreamWrappers{configure: func(config *agentcodex.Config) {
		config.TracerProvider = traces
		config.MeterProvider = meters
	}})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	_, err = provider.Start(ctx)
	require.NoError(t, err)
	options := agent.OpenOptions{Cwd: "/synthetic"}
	_, err = provider.Open(ctx, agent.OpenOptions{})
	require.Error(t, err)
	session, err := provider.Open(ctx, options)
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	require.Error(t, provider.Stop(ctx))
	require.NoError(t, session.Close(ctx))
	require.NoError(t, session.Close(ctx))
	resumed, err := provider.Resume(ctx, agent.ResumeOptions{SessionID: session.ID(), OpenOptions: options})
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
		assert.Positive(t, span.EndTime().Sub(span.StartTime()))
		assert.Contains(t, span.Attributes(), attribute.String("provider", "codex"))
		assert.Contains(t, span.Attributes(), attribute.String("outcome", result.outcome))
		if result.outcome == "rejected" {
			assert.Equal(t, codes.Error, span.Status().Code)
			assert.Empty(t, span.Status().Description)
			assert.Empty(t, span.Events())
		}
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
		assert.Equal(t, "codex", provider.AsString())
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
	host := telemetryRemoteSpan("11111111111111111111111111111111")
	explicit := telemetryRemoteSpan("22222222222222222222222222222222")
	ctx = trace.ContextWithRemoteSpanContext(ctx, host)
	explicitCtx := agent.WithTraceContext(ctx, agent.TraceContext{
		Traceparent: "00-" + explicit.TraceID().String() + "-" + explicit.SpanID().String() + "-01",
		Tracestate:  "vendor=parent",
	})
	frame := func(id int, text string, traceID trace.TraceID, span uint64, traceState string) string {
		return fmt.Sprintf(`>{"id":%d,"method":"turn/start","params":{"input":[{"text":%q,"type":"text"}],"threadId":"root-a"},"trace":{"traceparent":"00-%s-%016x-01"%s}}`,
			id, text, traceID, span, traceState)
	}
	const accepted = `"result":{"turn":{"id":"turn-a","items":[],"status":"inProgress"}}`
	frames := append([]string(nil), mockCodexInit...)
	frames = append(frames,
		`>{"id":92,"method":"thread/start","params":{"cwd":"/synthetic"}}`,
		`<{"id":92,"result":`+codexThreadStartResult("root-a")+`}`,
		`>{"id":93,"method":"thread/inject_items","params":{"items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"context"}]}],"threadId":"root-a"}}`,
		`<{"id":93,"result":{}}`,
		frame(94, "first", explicit.TraceID(), 6, `,"tracestate":"vendor=parent"`),
		`<{"id":94,`+accepted+`}`,
		codexTurnStartedFrame,
		frame(95, "adjust", host.TraceID(), 7, ""),
		`<{"id":95,`+accepted+`}`,
		frame(96, "blocked", host.TraceID(), 8, ""),
		`<{"id":96,"error":{"code":-32603,"message":"no active turn to steer"}}`,
		frame(97, "unknown", host.TraceID(), 9, ""),
		`<{"id":97,"result":{"turn":{"id":"","items":[],"status":"inProgress"}}}`,
		`>{"id":98,"method":"turn/interrupt","params":{"threadId":"root-a","turnId":"turn-a"}}`,
		`<{"id":98,"result":{}}`,
		commandApprovalRequest("approval-telemetry"),
		`>{"id":"approval-telemetry","result":{"decision":"accept"}}`,
	)
	frames = append(frames, codexCloseFrames(99, "root-a")...)
	provider := newTestProviderWithWrappers(t, prepareCodexPeer(t, frames...), codextypes.InitializeParams{
		ClientInfo: codextypes.ClientInfo{Name: "agent-codex-test", Version: "1"},
	}, testStreamWrappers{configure: func(config *agentcodex.Config) {
		config.TracerProvider = traces
		config.MeterProvider = meters
	}})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	stream, err := session.Events()
	require.NoError(t, err)

	_, err = session.StartOrSteer(ctx, agent.Input{})
	require.Error(t, err)
	_, err = session.(agent.Injector).Inject(ctx, textInput("context"))
	require.NoError(t, err)
	noWork, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	require.Equal(t, agent.InterruptNoActiveWork, noWork.Result)
	first, err := session.StartOrSteer(explicitCtx, textInput("first"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedNewWork, first.Result)
	second, err := session.StartOrSteer(ctx, textInput("adjust"))
	require.NoError(t, err)
	require.Equal(t, agent.InputAppliedCurrentWork, second.Result)
	badCarrier := agent.WithTraceContext(ctx, agent.TraceContext{Traceparent: "malformed"})
	blocked, err := session.StartOrSteer(badCarrier, textInput("blocked"))
	require.NoError(t, err)
	require.Equal(t, agent.InputNotApplied, blocked.Result)
	_, err = session.StartOrSteer(ctx, textInput("unknown"))
	require.Equal(t, agent.ErrorOutcomeUnknown, agent.CodeOf(err))
	interrupt, err := session.Interrupt(ctx, agent.InterruptOptions{})
	require.NoError(t, err)
	require.Equal(t, agent.InterruptAcknowledged, interrupt.Result)
	require.Equal(t, agent.StateRunning, nextState(t, ctx, stream))
	require.Equal(t, agent.StateRequiresAction, nextState(t, ctx, stream))
	request := nextInteractionRequested(t, ctx, stream)

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
	require.NoError(t, session.Close(ctx))
	require.NoError(t, provider.Stop(ctx))

	want := map[[2]string]uint64{
		{"start_or_steer", "rejected"}:                1,
		{"start_or_steer", "applied_new_work"}:        1,
		{"start_or_steer", "applied_current_work"}:    1,
		{"start_or_steer", "not_applied"}:             1,
		{"start_or_steer", "outcome_unknown"}:         1,
		{"session.inject", "native_accepted"}:         1,
		{"session.interrupt", "no_active_work"}:       1,
		{"session.interrupt", "control_acknowledged"}: 1,
		{"session.answer", "sent"}:                    1,
		{"session.answer", "rejected"}:                1,
		{"core.start", "success"}:                     1,
		{"core.open", "success"}:                      1,
		{"session.close", "success"}:                  1,
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
		if outcome.AsString() == "applied_new_work" {
			assert.Equal(t, explicit.WithTraceState(span.SpanContext().TraceState()), span.Parent())
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

func telemetryRemoteSpan(hex string) trace.SpanContext {
	traceID, _ := trace.TraceIDFromHex(hex)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: trace.SpanID{0xaa}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
}
