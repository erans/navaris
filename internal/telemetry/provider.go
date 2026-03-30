package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	otelTrace "go.opentelemetry.io/otel/trace"
)

var providerTracer = otel.Tracer("navaris.provider")

// providerDuration is lazily initialised on first use.
var providerDuration metric.Float64Histogram

func getProviderDuration() metric.Float64Histogram {
	if providerDuration == nil {
		h, _ := otel.Meter("navaris.provider").Float64Histogram(
			"provider.operation.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Provider operation duration"),
		)
		providerDuration = h
	}
	return providerDuration
}

// ProviderSpan starts a span for a provider operation and returns an end function.
// Call end(err) when the operation completes. If err is non-nil, the span records the error.
func ProviderSpan(ctx context.Context, backend, operation string) (context.Context, func(error)) {
	ctx, span := providerTracer.Start(ctx, fmt.Sprintf("provider.%s", operation),
		otelTrace.WithAttributes(
			attribute.String("provider.backend", backend),
		),
	)

	start := time.Now()

	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()

		RecordProviderDuration(ctx, backend, operation, time.Since(start))
	}
}

// RecordProviderDuration records a provider operation duration metric.
func RecordProviderDuration(ctx context.Context, backend, operation string, d time.Duration) {
	getProviderDuration().Record(ctx, d.Seconds(),
		metric.WithAttributes(
			attribute.String("backend", backend),
			attribute.String("operation", operation),
		),
	)
}

// SandboxCountCallback is the function signature for provider sandbox count callbacks.
type SandboxCountCallback func() map[string]int64

// RegisterSandboxCountGauge registers a provider.sandbox.count observable gauge.
// The callback returns a map of state→count (e.g. {"running": 3, "stopped": 1}).
// Call this once from each provider's init/constructor.
func RegisterSandboxCountGauge(backend string, cb SandboxCountCallback) {
	meter := otel.Meter("navaris.provider")
	gauge, _ := meter.Int64ObservableGauge("provider.sandbox.count",
		metric.WithDescription("Current sandbox count by state"),
	)
	meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for state, count := range cb() {
			o.ObserveInt64(gauge, count,
				metric.WithAttributes(
					attribute.String("backend", backend),
					attribute.String("state", state),
				),
			)
		}
		return nil
	}, gauge)
}
