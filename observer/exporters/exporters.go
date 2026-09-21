// Package exporters wires the OTEL_* exporter, propagator, and log-bridge
// configuration a sibling's command binary hands to its Agent. Library code
// never imports it, so a host that embeds a sibling compiles no exporter.
package exporters

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/savid/acp-go-core/observer"
)

// shutdownTimeout bounds provider flushing at exit.
const shutdownTimeout = 10 * time.Second

// Config names the sibling the telemetry describes.
type Config struct {
	// Vendor is the sibling's vendor key; it names the service and the
	// instrumentation scope.
	Vendor string
	// Version is the binary's build version.
	Version string
	// Logger is the base logger; a configured log exporter is joined to it.
	Logger *slog.Logger
}

// Bundle is what a command binary hands to its Agent options. A provider is
// nil when its signal is not configured.
type Bundle struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Logger         *slog.Logger
	shutdowns      []func(context.Context) error
}

// Shutdown flushes every configured provider, newest first, within a bounded
// time.
func (b Bundle) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()

	var errs []error
	for _, shutdown := range slices.Backward(b.shutdowns) {
		errs = append(errs, shutdown(ctx))
	}

	return errors.Join(errs...)
}

// Configure reads the OTEL_* environment and builds the providers it enables.
// A failure after a provider was built shuts that provider down again.
func Configure(ctx context.Context, config Config) (bundle Bundle, err error) {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	bundle = Bundle{Propagator: autoprop.NewTextMapPropagator(), Logger: config.Logger}

	defer func() {
		if err != nil {
			_ = bundle.Shutdown(ctx)
			bundle = Bundle{}
		}
	}()

	res, err := telemetryResource(ctx, config)
	if err != nil {
		return Bundle{}, err
	}

	if signalEnabled("OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", true) {
		exporter, err := autoexport.NewSpanExporter(ctx)
		if err != nil {
			return bundle, err
		}

		if !autoexport.IsNoneSpanExporter(exporter) {
			provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exporter))
			bundle.TracerProvider = provider
			bundle.shutdowns = append(bundle.shutdowns, provider.Shutdown)
		}
	}

	if signalEnabled("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", true) {
		reader, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			return bundle, err
		}

		if !autoexport.IsNoneMetricReader(reader) {
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(reader))
			bundle.MeterProvider = provider
			bundle.shutdowns = append(bundle.shutdowns, provider.Shutdown)
		}
	}

	if signalEnabled("OTEL_LOGS_EXPORTER", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", false) {
		exporter, err := autoexport.NewLogExporter(ctx)
		if err != nil {
			return bundle, err
		}

		if !autoexport.IsNoneLogExporter(exporter) {
			provider := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)))
			handler := otelslog.NewHandler(
				observer.InstrumentationName(config.Vendor),
				otelslog.WithLoggerProvider(provider),
				otelslog.WithVersion(config.Version),
			)
			bundle.Logger = slog.New(joinHandlers(config.Logger.Handler(), handler))
			bundle.shutdowns = append(bundle.shutdowns, provider.Shutdown)
		}
	}

	return bundle, nil
}

func telemetryResource(ctx context.Context, config Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{semconv.ServiceVersion(config.Version)}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		attrs = append(attrs, semconv.ServiceName("acp-go-"+config.Vendor))
	}

	return resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(),
	)
}

// signalEnabled follows the OTEL_* precedence: an explicit exporter name wins,
// then a signal-specific endpoint, then for traces and metrics the generic
// OTLP settings.
func signalEnabled(exporterEnv, endpointEnv string, genericOTLP bool) bool {
	if value := os.Getenv(exporterEnv); value != "" {
		return value != "none"
	}

	if os.Getenv(endpointEnv) != "" {
		return true
	}

	if !genericOTLP {
		return false
	}

	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_HEADERS") != ""
}

type joinedHandler struct {
	handlers []slog.Handler
}

func joinHandlers(handlers ...slog.Handler) slog.Handler {
	joined := joinedHandler{handlers: make([]slog.Handler, 0, len(handlers))}
	for _, handler := range handlers {
		if handler != nil {
			joined.handlers = append(joined.handlers, handler)
		}
	}

	return joined
}

func (h joinedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (h joinedHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error

	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			errs = append(errs, handler.Handle(ctx, record))
		}
	}

	return errors.Join(errs...)
}

func (h joinedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}

	return joinedHandler{handlers: handlers}
}

func (h joinedHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}

	return joinedHandler{handlers: handlers}
}
