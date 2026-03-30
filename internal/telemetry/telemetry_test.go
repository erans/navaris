package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestInit_NoOpWhenEndpointEmpty(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if telemetry.Enabled() {
		t.Error("Enabled() = true, want false when endpoint is empty")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
}

func TestInit_EnabledWithEndpoint(t *testing.T) {
	// Use a non-routable endpoint — Init creates exporters but doesn't connect immediately.
	cfg := telemetry.Config{
		Endpoint:    "localhost:4317",
		Protocol:    "grpc",
		ServiceName: "test-service",
	}
	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		shutdown(ctx)
	})

	if !telemetry.Enabled() {
		t.Error("Enabled() = false, want true when endpoint is set")
	}

	// Verify global tracer provider is an SDK provider, not no-op.
	tp := otel.GetTracerProvider()
	if _, ok := tp.(*sdktrace.TracerProvider); !ok {
		t.Errorf("global TracerProvider = %T, want *sdktrace.TracerProvider", tp)
	}
}

func TestInit_HTTPProtocol(t *testing.T) {
	cfg := telemetry.Config{
		Endpoint:    "localhost:4318",
		Protocol:    "http",
		ServiceName: "test-service",
	}
	shutdown, err := telemetry.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		shutdown(ctx)
	})

	if !telemetry.Enabled() {
		t.Error("Enabled() = false, want true")
	}
}
