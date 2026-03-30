# Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add OpenTelemetry-based metrics and distributed tracing to navarisd, exported via OTLP, with zero overhead when disabled.

**Architecture:** Pure OTel SDK with global no-op providers when disabled. HTTP middleware for request metrics/traces, span instrumentation in service and provider layers, observable gauges for dispatcher state. Telemetry bootstrap in `internal/telemetry`, middleware in `internal/api/metrics.go`.

**Tech Stack:** `go.opentelemetry.io/otel` SDK, OTLP gRPC/HTTP exporters, `go.opentelemetry.io/contrib/instrumentation/runtime`

**Spec:** `docs/superpowers/specs/2026-03-30-observability-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/telemetry/telemetry.go` | Config, Init, Shutdown, Enabled — telemetry lifecycle |
| `internal/telemetry/telemetry_test.go` | Tests for Init (no-op and enabled modes) |
| `internal/telemetry/provider.go` | Provider instrumentation helpers: span + duration recording |
| `internal/telemetry/provider_test.go` | Tests for provider helpers |
| `internal/api/metrics.go` | HTTP metrics middleware + tracing middleware + exclusion logic |
| `internal/api/metrics_test.go` | Tests for both middleware (in-memory OTel readers) |
| `internal/api/server.go` | Modified: conditionally insert telemetry middleware |
| `internal/service/project.go` | Modified: add spans to public methods |
| `internal/service/sandbox.go` | Modified: add spans to public methods |
| `internal/service/snapshot.go` | Modified: add spans to public methods |
| `internal/service/image.go` | Modified: add spans to public methods |
| `internal/service/session.go` | Modified: add spans to public methods |
| `internal/service/operation.go` | Modified: add spans to public methods |
| `internal/worker/dispatcher.go` | Modified: add observable gauges + operations counter |
| `internal/worker/dispatcher_test.go` | Modified: add tests for metrics registration |
| `cmd/navarisd/main.go` | Modified: add CLI flags, call Init, fix shutdown ordering |
| `go.mod` / `go.sum` | Modified: add OTel dependencies |

---

### Task 1: Add OTel SDK Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add OTel SDK and exporter dependencies**

```bash
cd /home/eran/work/navaris
go get go.opentelemetry.io/otel/sdk@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp@latest
go get go.opentelemetry.io/contrib/instrumentation/runtime@latest
```

- [ ] **Step 2: Tidy and verify**

```bash
go mod tidy
go build ./...
```
Expected: builds with no errors. `go.mod` now lists all OTel packages as direct or indirect dependencies.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add OpenTelemetry SDK dependencies for observability"
```

---

### Task 2: Telemetry Bootstrap Package

**Files:**
- Create: `internal/telemetry/telemetry.go`
- Create: `internal/telemetry/telemetry_test.go`

- [ ] **Step 1: Write tests for telemetry Init**

```go
// internal/telemetry/telemetry_test.go
package telemetry_test

import (
	"context"
	"testing"

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
	t.Cleanup(func() { shutdown(context.Background()) })

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
	t.Cleanup(func() { shutdown(context.Background()) })

	if !telemetry.Enabled() {
		t.Error("Enabled() = false, want true")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/telemetry/ -v -count=1
```
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Write telemetry.go implementation**

```go
// internal/telemetry/telemetry.go
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
		tp.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry metric exporter: %w", err)
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
		tp.Shutdown(ctx)
		mp.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry runtime: %w", err)
	}

	enabled = true

	shutdown := func(ctx context.Context) error {
		enabled = false
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}

	return shutdown, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/telemetry/ -v -count=1
```
Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/
git commit -m "feat: add telemetry bootstrap package with Init/Shutdown/Enabled"
```

---

### Task 3: HTTP Metrics and Tracing Middleware

**Files:**
- Create: `internal/api/metrics.go`
- Create: `internal/api/metrics_test.go`

- [ ] **Step 1: Write tests for HTTP middleware**

The test uses OTel SDK in-memory readers to verify metrics and spans without a real collector.

```go
// internal/api/metrics_test.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
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

	req := httptest.NewRequest("GET", "/v1/events", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	spans := spanExp.GetSpans()
	if len(spans) != 0 {
		t.Errorf("got %d spans for excluded endpoint, want 0", len(spans))
	}

	var rm metricdata.ResourceMetrics
	reader.Collect(context.Background(), &rm)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "http.server.request.duration" {
				t.Error("http.server.request.duration recorded for excluded endpoint")
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
	if span.Status.Code != 2 { // codes.Error = 2
		t.Errorf("span status = %d, want 2 (Error) for 5xx", span.Status.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ -run "TestMetrics|TestTracing|TestMiddleware_Excludes" -v -count=1
```
Expected: FAIL — `newMetricsMiddleware` and `newTracingMiddleware` not defined.

- [ ] **Step 3: Write metrics.go implementation**

```go
// internal/api/metrics.go
package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	otelTrace "go.opentelemetry.io/otel/trace"
)

func isExcluded(path string) bool {
	if path == "/v1/events" {
		return true
	}
	// Match /v1/sandboxes/{id}/exec
	if strings.HasSuffix(path, "/exec") && strings.HasPrefix(path, "/v1/sandboxes/") {
		return true
	}
	return false
}

// routeFromPattern extracts the route from r.Pattern, stripping the method prefix.
// e.g. "GET /v1/sandboxes/{id}" → "/v1/sandboxes/{id}"
func routeFromPattern(r *http.Request) string {
	p := r.Pattern
	if p == "" {
		return "unknown"
	}
	if idx := strings.Index(p, " "); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// statusCapturingWriter wraps http.ResponseWriter to capture the status code.
// Note: the existing statusCapture in middleware.go serves loggingMiddleware;
// this type adds Write() tracking needed by the metrics middleware.
type statusCapturingWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	if !w.written {
		w.code = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.code = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

func newMetricsMiddleware() (func(http.Handler) http.Handler, error) {
	meter := otel.Meter("navaris.http")

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("HTTP request duration"),
	)
	if err != nil {
		return nil, fmt.Errorf("create duration histogram: %w", err)
	}

	activeReqs, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("Number of in-flight HTTP requests"),
	)
	if err != nil {
		return nil, fmt.Errorf("create active_requests counter: %w", err)
	}

	reqSize, err := meter.Float64Histogram("http.server.request.size",
		metric.WithUnit("By"),
		metric.WithDescription("HTTP request body size"),
	)
	if err != nil {
		return nil, fmt.Errorf("create request.size histogram: %w", err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isExcluded(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			method := r.Method

			activeReqs.Add(r.Context(), 1, metric.WithAttributes(
				attribute.String("method", method),
			))

			sc := &statusCapturingWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sc, r)

			route := routeFromPattern(r)
			elapsed := time.Since(start).Seconds()

			attrs := metric.WithAttributes(
				attribute.String("method", method),
				attribute.String("route", route),
				attribute.Int("status_code", sc.code),
			)
			duration.Record(r.Context(), elapsed, attrs)

			if r.ContentLength > 0 {
				reqSize.Record(r.Context(), float64(r.ContentLength),
					metric.WithAttributes(
						attribute.String("method", method),
						attribute.String("route", route),
					),
				)
			}

			activeReqs.Add(r.Context(), -1, metric.WithAttributes(
				attribute.String("method", method),
			))
		})
	}, nil
}

func newTracingMiddleware() func(http.Handler) http.Handler {
	tracer := otel.Tracer("navaris.http")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isExcluded(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// Extract incoming trace context from headers (W3C traceparent).
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			ctx, span := tracer.Start(ctx, fmt.Sprintf("HTTP %s", r.Method),
				otelTrace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
				),
			)
			defer span.End()

			sc := &statusCapturingWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sc, r.WithContext(ctx))

			route := routeFromPattern(r)
			span.SetName(fmt.Sprintf("HTTP %s %s", r.Method, route))
			span.SetAttributes(
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", sc.code),
			)

			if sc.code >= 500 {
				span.SetStatus(codes.Error, http.StatusText(sc.code))
			}
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/api/ -run "TestMetrics|TestTracing|TestMiddleware_Excludes" -v -count=1
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/metrics.go internal/api/metrics_test.go
git commit -m "feat: add HTTP metrics and tracing middleware"
```

---

### Task 4: Wire Middleware into Server

**Files:**
- Modify: `internal/api/server.go`

- [ ] **Step 1: Import telemetry package and conditionally add middleware**

In `internal/api/server.go`, modify the `Handler()` method. After the existing middleware chain (line 97-101), insert telemetry middleware before `requestIDMiddleware` when telemetry is enabled:

```go
// Replace lines 97-103 in server.go with:

	// Apply middleware chain: requestID -> auth -> logging -> mux
	var handler http.Handler = mux
	handler = loggingMiddleware(s.log)(handler)
	handler = authMiddleware(s.cfg.AuthToken)(handler)
	handler = requestIDMiddleware(handler)

	// Telemetry middleware (outermost when enabled):
	// tracing -> metrics -> requestID -> auth -> logging -> mux
	if telemetry.Enabled() {
		mw, err := newMetricsMiddleware()
		if err != nil {
			s.log.Error("failed to create metrics middleware", "error", err)
		} else {
			handler = mw(handler)
		}
		handler = newTracingMiddleware()(handler)
	}

	return handler
```

Add the import:
```go
"github.com/navaris/navaris/internal/telemetry"
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```
Expected: builds successfully.

- [ ] **Step 3: Run existing tests to verify no regressions**

```bash
go test ./internal/api/ -v -count=1
```
Expected: all existing tests pass. Telemetry middleware is not added because `telemetry.Enabled()` returns false (no Init called in tests).

- [ ] **Step 4: Commit**

```bash
git add internal/api/server.go
git commit -m "feat: conditionally wire telemetry middleware into HTTP chain"
```

---

### Task 5: Service Layer Tracing

**Files:**
- Modify: `internal/service/project.go`
- Modify: `internal/service/sandbox.go`
- Modify: `internal/service/snapshot.go`
- Modify: `internal/service/image.go`
- Modify: `internal/service/session.go`
- Modify: `internal/service/operation.go`

Each public service method gets the same pattern. Only instrument the public API methods (called from HTTP handlers), not the private `handle*` methods (those run in the dispatcher with a different context and are covered by dispatcher metrics).

- [ ] **Step 1: Add tracing to ProjectService methods**

Add these imports to `internal/service/project.go`:
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/codes"
```

Add span instrumentation to each public method. Pattern (add as first two lines of each method body):

```go
func (s *ProjectService) Create(ctx context.Context, name string, metadata map[string]any) (*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateProject")
	defer span.End()

	// ... existing code ...

	// Before each error return, add:
	// span.RecordError(err)
	// span.SetStatus(codes.Error, err.Error())
}
```

Apply this pattern to all 6 `ProjectService` methods: `Create`, `Get`, `GetByName`, `List`, `Update`, `Delete`.

- [ ] **Step 2: Add tracing to SandboxService methods**

Apply the same pattern to the 7 public `SandboxService` methods: `Create`, `CreateFromSnapshot`, `Get`, `List`, `Start`, `Stop`, `Destroy`.

For methods that create sandbox resources, add correlation attributes:
```go
span.SetAttributes(attribute.String("sandbox.id", sbx.ID))
```

For `SandboxService`, the `defaultBackend` field is available on the struct — add it as an attribute where the backend is known:
```go
span.SetAttributes(attribute.String("provider.backend", s.defaultBackend))
```

- [ ] **Step 3: Add tracing to SnapshotService methods**

Apply to 5 methods: `Create`, `Get`, `ListBySandbox`, `Restore`, `Delete`.

Add correlation attributes where available:
```go
span.SetAttributes(attribute.String("snapshot.id", id))
```

- [ ] **Step 4: Add tracing to ImageService methods**

Apply to 5 methods: `PromoteSnapshot`, `Register`, `Get`, `List`, `Delete`.

- [ ] **Step 5: Add tracing to SessionService methods**

Apply to 4 methods: `Create`, `Get`, `ListBySandbox`, `Destroy`.

- [ ] **Step 6: Add tracing to OperationService methods**

Apply to 3 methods: `Get`, `List`, `Cancel`.

- [ ] **Step 7: Verify build and run existing service tests**

```bash
go build ./internal/service/...
go test ./internal/service/ -v -count=1
```
Expected: builds and all existing tests pass. OTel no-op global providers are used in tests (no Init called), so spans are silently discarded.

- [ ] **Step 8: Commit**

```bash
git add internal/service/
git commit -m "feat: add OTel span instrumentation to service layer"
```

---

### Task 6: Provider Instrumentation Helpers

**Files:**
- Create: `internal/telemetry/provider.go`
- Create: `internal/telemetry/provider_test.go`

- [ ] **Step 1: Write tests for provider helpers**

```go
// internal/telemetry/provider_test.go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/telemetry/ -run "TestProvider|TestRecordProvider" -v -count=1
```
Expected: FAIL — `ProviderSpan` and `RecordProviderDuration` not defined.

- [ ] **Step 3: Write provider.go implementation**

```go
// internal/telemetry/provider.go
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
```

The `RegisterSandboxCountGauge` function is called from each provider's constructor. The callback implementation is provider-specific:

**Firecracker** (in `firecracker.go` `NewProvider`): iterate `p.vms` map, classify by `VMInfo` fields:
- `running`: `info.PID > 0 && processAlive(info.PID)`
- `stopping`: `info.Stopping`
- `stopped`: all others

**Incus** (in `incus.go` constructor): query the Incus daemon for instance list, map status strings:
- `Running` → `running`
- `Stopped`/`Frozen` → `stopped`
- `Stopping`/`Aborting`/`Freezing` → `stopping`
- Other transient states → `stopped`

These per-provider integrations should be added as part of this task when wiring `ProviderSpan` calls into the providers.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/telemetry/ -run "TestProvider|TestRecordProvider" -v -count=1
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/telemetry/provider.go internal/telemetry/provider_test.go
git commit -m "feat: add provider instrumentation helpers for spans and duration"
```

---

### Task 7: Dispatcher Metrics

**Files:**
- Modify: `internal/worker/dispatcher.go`
- Modify: `internal/worker/dispatcher_test.go`

- [ ] **Step 1: Write test for dispatcher metrics registration**

Add to `internal/worker/dispatcher_test.go`:

```go
func TestDispatcher_MetricsRegistered(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { mp.Shutdown(context.Background()) })

	store := &mockOpStore{ops: make(map[string]*domain.Operation)}
	bus := eventbus.New(64)
	d := worker.NewDispatcher(store, bus, 4)
	d.Start()
	t.Cleanup(func() { d.Stop() })

	// Trigger a collection to exercise the gauge callbacks.
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	names := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}

	for _, want := range []string{"dispatcher.queue.depth", "dispatcher.inflight"} {
		if !names[want] {
			t.Errorf("metric %q not found", want)
		}
	}
}
```

Additional imports needed in the test file (add to the existing import block):
```go
"go.opentelemetry.io/otel"
sdkmetric "go.opentelemetry.io/otel/sdk/metric"
"go.opentelemetry.io/otel/sdk/metric/metricdata"
```

The test reuses the existing `mockOpStore` type and `eventbus.New()` pattern already used by other tests in this file.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/worker/ -run TestDispatcher_MetricsRegistered -v -count=1
```
Expected: FAIL — metrics are not registered yet.

- [ ] **Step 3: Add metrics to dispatcher.go**

Add these imports to `internal/worker/dispatcher.go`:
```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/attribute"
"go.opentelemetry.io/otel/metric"
```

Add an `opsTotal` counter field to the `Dispatcher` struct:
```go
type Dispatcher struct {
	// ... existing fields ...
	opsTotal metric.Int64Counter
}
```

In `NewDispatcher`, after creating the struct, register metrics:
```go
func NewDispatcher(opStore domain.OperationStore, events domain.EventBus, concurrency int) *Dispatcher {
	// ... existing code ...

	d := &Dispatcher{
		// ... existing fields ...
	}

	// Register telemetry instruments.
	meter := otel.Meter("navaris.dispatcher")

	queueDepth, _ := meter.Int64ObservableGauge("dispatcher.queue.depth",
		metric.WithDescription("Current number of queued operations"),
	)
	inflight, _ := meter.Int64ObservableGauge("dispatcher.inflight",
		metric.WithDescription("Currently executing operations"),
	)
	meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(queueDepth, int64(len(d.queue)))
		o.ObserveInt64(inflight, int64(len(d.sem)))
		return nil
	}, queueDepth, inflight)

	d.opsTotal, _ = meter.Int64Counter("dispatcher.operations.total",
		metric.WithDescription("Completed operations"),
	)

	return d
}
```

In the `run` method, after each terminal state transition, increment the counter. Add after the succeeded/failed/cancelled updates:

In `run()` after `op.State = domain.OpSucceeded` block (around line 191-197):
```go
d.opsTotal.Add(ctx, 1,
	metric.WithAttributes(
		attribute.String("type", op.Type),
		attribute.String("status", string(op.State)),
	),
)
```

Similarly in the `fail()` method and `cancel()` method, add:
```go
d.opsTotal.Add(context.Background(), 1,
	metric.WithAttributes(
		attribute.String("type", op.Type),
		attribute.String("status", string(op.State)),
	),
)
```

And in the `run()` method's pre-run cancellation block (around line 148-156):
```go
d.opsTotal.Add(context.Background(), 1,
	metric.WithAttributes(
		attribute.String("type", op.Type),
		attribute.String("status", string(op.State)),
	),
)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/worker/ -v -count=1
```
Expected: all tests PASS, including the new metrics test.

- [ ] **Step 5: Commit**

```bash
git add internal/worker/dispatcher.go internal/worker/dispatcher_test.go
git commit -m "feat: add dispatcher observable gauges and operations counter"
```

---

### Task 8: Main.go CLI Flags and Shutdown Integration

**Files:**
- Modify: `cmd/navarisd/main.go`

- [ ] **Step 1: Add telemetry config fields**

Add to the `config` struct in `cmd/navarisd/main.go`:
```go
type config struct {
	// ... existing fields ...
	otlpEndpoint string
	otlpProtocol string
	serviceName  string
}
```

- [ ] **Step 2: Add CLI flags**

In `parseFlags()`, add after the existing flags:
```go
flag.StringVar(&cfg.otlpEndpoint, "otlp-endpoint", "", "OTLP collector endpoint (e.g. localhost:4317); empty disables telemetry")
flag.StringVar(&cfg.otlpProtocol, "otlp-protocol", "grpc", "OTLP transport protocol: grpc or http")
flag.StringVar(&cfg.serviceName, "service-name", "navarisd", "service name in telemetry data")
```

- [ ] **Step 3: Call telemetry.Init in run()**

Add the import:
```go
"github.com/navaris/navaris/internal/telemetry"
```

In `run()`, after `slog.SetDefault(logger)` (line 71) and before opening the store, add:
```go
	telemetryShutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		Endpoint:    cfg.otlpEndpoint,
		Protocol:    cfg.otlpProtocol,
		ServiceName: cfg.serviceName,
	})
	if err != nil {
		return fmt.Errorf("telemetry init: %w", err)
	}
	if telemetry.Enabled() {
		logger.Info("telemetry enabled", "endpoint", cfg.otlpEndpoint, "protocol", cfg.otlpProtocol)
	}
```

- [ ] **Step 4: Fix shutdown ordering**

In the shutdown sequence (after line 212 `disp.Stop()`), add telemetry shutdown with a dedicated context:
```go
	// Telemetry shutdown — after all components have stopped.
	telShutdownCtx, telCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer telCancel()
	if err := telemetryShutdown(telShutdownCtx); err != nil {
		logger.Error("telemetry shutdown error", "error", err)
	}
```

The full shutdown block should now be:
```go
	// Shutdown sequence
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}
	gc.Stop()
	disp.Stop()

	telShutdownCtx, telCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer telCancel()
	if err := telemetryShutdown(telShutdownCtx); err != nil {
		logger.Error("telemetry shutdown error", "error", err)
	}

	logger.Info("stopped")
	return nil
```

- [ ] **Step 5: Verify build**

```bash
go build ./cmd/navarisd/
```
Expected: builds successfully.

- [ ] **Step 6: Verify help output includes new flags**

```bash
go run ./cmd/navarisd/ -h 2>&1 | grep -E "otlp|service-name"
```
Expected: shows `-otlp-endpoint`, `-otlp-protocol`, `-service-name` flags.

- [ ] **Step 7: Commit**

```bash
git add cmd/navarisd/main.go
git commit -m "feat: add telemetry CLI flags and shutdown integration"
```

---

### Task 9: Integration Verification

**Files:** none (verification only)

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1
```
Expected: all tests PASS. No regressions from telemetry changes. Tests run without an OTLP endpoint, so global no-op providers are used.

- [ ] **Step 2: Verify clean build with all tags**

```bash
go build -tags firecracker ./cmd/navarisd/
go vet ./...
```
Expected: no errors or warnings.

- [ ] **Step 3: Verify binary starts without telemetry (default)**

```bash
go run ./cmd/navarisd/ -db-path :memory: &
PID=$!
sleep 1
curl -s http://localhost:8080/v1/health | head -c 200
kill $PID 2>/dev/null
```
Expected: server starts, health check responds, no telemetry-related log output.

- [ ] **Step 4: Verify binary starts with telemetry endpoint**

```bash
go run ./cmd/navarisd/ -db-path :memory: -otlp-endpoint localhost:4317 &
PID=$!
sleep 1
# Check for telemetry enabled log line
kill $PID 2>/dev/null
```
Expected: logs show `"telemetry enabled"` with endpoint info. Server starts normally even if no collector is running (OTLP client handles reconnection).
