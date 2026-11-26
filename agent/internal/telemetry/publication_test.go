package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/agent/internal/eventstream"
	"github.com/zoumo/agent-sdk-go/internal/queue"
)

func TestPublicationSnapshotsDeliveredEvent(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { assert.NoError(t, traces.Shutdown(context.Background())) })
	instruments := New("codex", traces, metricnoop.NewMeterProvider(), nil)
	var reader agent.EventStream
	var published int64
	instruments.published = publicationCounter{
		Int64Counter: instruments.published,
		onAdd: func(count int64) {
			published += count
			if published != 1 {
				return
			}
			// Consume and annotate the event between the counter update and the span.
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			event, err := reader.Next(ctx)
			require.NoError(t, err)
			native := event.Meta["codex"].(map[string]any)
			native["method"] = "consumer method"
			native["nativeType"] = "consumer type"
			delete(native, "nativeSubtype")
			delete(event.Meta, "codex")
		},
	}
	stream := eventstream.New(eventstream.Hooks{PreparePublication: instruments.PreparePublication})
	t.Cleanup(func() {
		stream.ObservationEnded(nil)
		_, err := stream.Close(context.Background())
		assert.NoError(t, err)
	})
	var err error
	reader, err = stream.Claim()
	require.NoError(t, err)

	event := agent.Event{
		SessionID: "session-1",
		Payload:   agent.UnsupportedNativeEventPayload{AgentType: "codex"},
		Meta: map[string]any{"codex": map[string]any{
			"method":        "original method",
			"nativeType":    "original type",
			"nativeSubtype": "original subtype",
		}},
	}
	require.NoError(t, stream.Publish([]agent.Event{event}))
	stream.ObservationEnded(nil)
	<-stream.Done()
	require.ErrorIs(t, stream.Publish([]agent.Event{event}), queue.ErrClosed)
	assert.Equal(t, int64(1), published)
	spans := recorder.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "agent.unsupported_native_event", spans[0].Name())
	assert.ElementsMatch(t, []attribute.KeyValue{
		attribute.String("provider", "codex"),
		attribute.String("session.id", "session-1"),
		attribute.String("native.method", "original method"),
		attribute.String("native.type", "original type"),
		attribute.String("native.subtype", "original subtype"),
	}, spans[0].Attributes())
}

type publicationCounter struct {
	metric.Int64Counter
	onAdd func(int64)
}

func (c publicationCounter) Add(_ context.Context, count int64, _ ...metric.AddOption) {
	c.onAdd(count)
}
