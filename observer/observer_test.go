package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNilObserverIsNoop(t *testing.T) {
	t.Parallel()

	var observer *Observer

	ctx, finish := observer.StartACP(context.Background(), nil, "initialize")
	require.NotNil(t, ctx)
	finish(errors.New("ignored"))

	_, finishPrompt := observer.StartPrompt(ctx, nil, "model")
	finishPrompt(PromptResult{})
	observer.RecordProcessExit(ctx, "exit", nil)
	observer.AddActiveSession(ctx, 1)
	observer.RecordRawMessageEmitFailure(ctx, errors.New("x"))
}

func TestObserverRecordsSpansAndMetrics(t *testing.T) {
	t.Parallel()

	exporter := tracetest.NewInMemoryExporter()
	reader := sdkmetric.NewManualReader()
	observer := New(Config{
		Vendor:         "pi",
		NativeClient:   "rpc",
		Version:        "test",
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})

	ctx := context.Background()

	_, finish := observer.StartACP(ctx, map[string]any{"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}, "session/new")
	finish(nil)

	_, finishPrompt := observer.StartPrompt(ctx, nil, "model")
	finishPrompt(PromptResult{StopReason: "end_turn", InputTokens: 3, OutputTokens: 2})

	_, finishStore := observer.StartSessionStore(ctx, "replace")
	finishStore(errors.New("store down"))

	observer.RecordProcessExit(ctx, "exit", nil)
	observer.recordSessionStore(ctx, time.Now(), "load", nil)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)
	require.Equal(t, "0af7651916cd43dd8448eb211c80319c", spans[0].SpanContext.TraceID().String(), "trace context is extracted from _meta")

	var metrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &metrics))

	names := map[string]bool{}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			names[metric.Name] = true
		}
	}

	require.True(t, names["acp_go_pi.acp.request.count"])
	require.True(t, names["acp_go_pi.session.prompt.count"])
	require.True(t, names["gen_ai.client.token.usage"])
	require.True(t, names["acp_go_pi.session_store.error.count"])
}

func TestErrorType(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", errorType(nil))
	require.Equal(t, "context.Canceled", errorType(context.Canceled))
	require.Equal(t, "context.DeadlineExceeded", errorType(context.DeadlineExceeded))
	require.Equal(t, "*errors.errorString", errorType(errors.New("x")))
}
