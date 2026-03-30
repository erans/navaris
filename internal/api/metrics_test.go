package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func setupTestTelemetry(t *testing.T) (*tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()

	spanExp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExp))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(context.Background()) })

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	return spanExp, reader
}

func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	_, reader := setupTestTelemetry(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw, err := newMetricsMiddleware()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := mw(handler)

	// Use a mux so r.Pattern is populated.
	mux := http.NewServeMux()
	mux.Handle("GET /v1/test", wrapped)

	req := httptest.NewRequest("GET", "/v1/test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "http.server.request.duration" {
				found = true
			}
		}
	}
	if !found {
		t.Error("http.server.request.duration metric not found")
	}
}

func TestTracingMiddleware_CreatesSpan(t *testing.T) {
	spanExp, _ := setupTestTelemetry(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := newTracingMiddleware()(handler)

	mux := http.NewServeMux()
	mux.Handle("GET /v1/sandboxes", wrapped)

	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	spans := spanExp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	span := spans[0]
	if !strings.HasPrefix(span.Name, "HTTP GET") {
		t.Errorf("span name = %q, want prefix 'HTTP GET'", span.Name)
	}
}

func TestMiddleware_ExcludesEventsEndpoint(t *testing.T) {
	spanExp, reader := setupTestTelemetry(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw, err := newMetricsMiddleware()
	if err != nil {
		t.Fatal(err)
	}
	wrapped := newTracingMiddleware()(mw(handler))

	for _, path := range []string{"/v1/events", "/v1/sandboxes/abc-123/exec"} {
		spanExp.Reset()

		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		spans := spanExp.GetSpans()
		if len(spans) != 0 {
			t.Errorf("%s: got %d spans for excluded endpoint, want 0", path, len(spans))
		}

		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "http.server.request.duration" {
					t.Errorf("%s: http.server.request.duration recorded for excluded endpoint", path)
				}
			}
		}
	}
}

func TestTracingMiddleware_SetsErrorStatusOn5xx(t *testing.T) {
	spanExp, _ := setupTestTelemetry(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	wrapped := newTracingMiddleware()(handler)

	mux := http.NewServeMux()
	mux.Handle("GET /v1/test", wrapped)

	req := httptest.NewRequest("GET", "/v1/test", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	spans := spanExp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	span := spans[0]
	// In the Go OTel SDK, codes.Error = 1 (codes.Ok = 2); this differs from the
	// OTLP wire encoding where Error=2. We compare against codes.Error directly.
	if span.Status.Code != codes.Error {
		t.Errorf("span status = %d, want %d (Error) for 5xx", span.Status.Code, codes.Error)
	}
}
