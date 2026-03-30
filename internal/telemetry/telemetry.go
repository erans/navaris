package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
)

var enabled bool

// Enabled reports whether telemetry export is active.
// Written once during Init before any handler is registered; read-only thereafter.
func Enabled() bool { return enabled }

// Config holds telemetry configuration.
type Config struct {
	Endpoint    string // OTLP collector endpoint; empty disables telemetry.
	Protocol    string // "grpc" (default) or "http".
	ServiceName string // Service name in telemetry data.
}

// Init initialises OTel providers and returns a shutdown function.
// When cfg.Endpoint is empty, global providers remain no-op.
func Init(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }

	if cfg.Endpoint == "" {
		return noop, nil
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "navarisd"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	// Trace exporter
	var traceExp sdktrace.SpanExporter
	switch cfg.Protocol {
	case "http":
		traceExp, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	default:
		traceExp, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	// Metric exporter
	var metricExp sdkmetric.Exporter
	switch cfg.Protocol {
	case "http":
		metricExp, err = otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
			otlpmetrichttp.WithInsecure(),
		)
	default:
		metricExp, err = otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
			otlpmetricgrpc.WithInsecure(),
		)
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("telemetry metric exporter: %w", err), tp.Shutdown(ctx))
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res),
	)

	// Set global providers
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Runtime instrumentation (after globals are set)
	if err := otelruntime.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("telemetry runtime: %w", err),
			tp.Shutdown(ctx),
			mp.Shutdown(ctx),
		)
	}

	enabled = true

	shutdown := func(ctx context.Context) error {
		enabled = false
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}

	return shutdown, nil
}
