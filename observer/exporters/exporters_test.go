package exporters

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigureWithoutExporters(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	logger := slog.New(slog.DiscardHandler)
	bundle, err := Configure(context.Background(), Config{Vendor: "test", Version: "test", Logger: logger})
	require.NoError(t, err)
	require.Nil(t, bundle.TracerProvider)
	require.Nil(t, bundle.MeterProvider)
	require.NotNil(t, bundle.Propagator)
	require.Same(t, logger, bundle.Logger)
	require.NoError(t, bundle.Shutdown(context.Background()))
}

func TestConfigureWithConsoleExporters(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_METRICS_EXPORTER", "console")
	t.Setenv("OTEL_LOGS_EXPORTER", "console")

	logger := slog.New(slog.DiscardHandler)
	bundle, err := Configure(context.Background(), Config{Vendor: "test", Version: "test", Logger: logger})
	require.NoError(t, err)
	require.NotNil(t, bundle.TracerProvider)
	require.NotNil(t, bundle.MeterProvider)
	require.NotSame(t, logger, bundle.Logger)
	require.True(t, bundle.Logger.Enabled(context.Background(), slog.LevelInfo))
	require.NoError(t, bundle.Shutdown(context.Background()))
}

func TestSignalEnabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	require.False(t, signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", true))

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	require.True(t, signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", true))
	require.False(t, signalEnabled("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", false))

	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "http://localhost:4318")
	require.True(t, signalEnabled("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", false))
}
