package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestProviderSpan_CreatesChildSpan(t *testing.T) {
	spanExp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExp))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(context.Background()) })

	ctx, span := otel.Tracer("test").Start(context.Background(), "parent")

	ctx, end := telemetry.ProviderSpan(ctx, "firecracker", "CreateSandbox")
	_ = ctx
	end(nil)
	span.End()

	spans := spanExp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}

	provSpan := spans[0] // child ends first
	if provSpan.Name != "provider.CreateSandbox" {
		t.Errorf("span name = %q, want %q", provSpan.Name, "provider.CreateSandbox")
	}
}

func TestRecordProviderDuration(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	telemetry.RecordProviderDuration(context.Background(), "incus", "StopSandbox", 150*time.Millisecond)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "provider.operation.duration" {
				found = true
			}
		}
	}
	if !found {
		t.Error("provider.operation.duration metric not found")
	}
}
