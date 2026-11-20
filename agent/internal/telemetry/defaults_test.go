package telemetry_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/agent/codex"
)

func TestLifecycleDefaultProviders(t *testing.T) {
	ctx := t.Context()
	// OTel's initial globals delegate on first install. A subprocess leaves
	// those globals intact for repeated runs and other tests in this package.
	const child = "AGENT_SDK_TEST_GLOBAL_OTEL"
	if os.Getenv(child) != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		command := exec.CommandContext(ctx, executable, "-test.run=^TestLifecycleDefaultProviders$")
		command.Env = append(command.Environ(), child+"=1", "GORACE=atexit_sleep_ms=0")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	codex := agentcodex.New(agentcodex.Config{})
	claude := agentclaude.New(agentclaude.Config{})
	require.NoError(t, codex.Stop(ctx))
	require.NoError(t, claude.Stop(ctx))

	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, codex.Stop(ctx))
	require.NoError(t, claude.Stop(ctx))

	require.NoError(t, agentcodex.New(agentcodex.Config{
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
	}).Stop(ctx))
	require.NoError(t, agentclaude.New(agentclaude.Config{
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())),
	}).Stop(ctx))

	spans := recorder.Ended()
	require.Len(t, spans, 2)
	for _, span := range spans {
		assert.Equal(t, "agent.core.stop", span.Name())
	}
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	points := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64]).DataPoints
	require.Len(t, points, 3)
	for _, point := range points {
		assert.Equal(t, uint64(1), point.Count)
	}
}
