# Observability: Metrics and Tracing

## Goal

Add OpenTelemetry-based metrics and distributed tracing to navarisd, exported via OTLP. When enabled, operators get request latency histograms, sandbox/operation counts, dispatcher queue depth, and end-to-end traces through the API, service, and provider layers. When disabled (no endpoint configured), behavior is unchanged — zero overhead.

## Success Criteria

- `-otlp-endpoint` flag enables telemetry export; omitting it keeps current behavior
- Grafana or Jaeger shows HTTP request latency, error rates, and trace waterfalls
- Provider operation timing is visible per-backend
- Dispatcher queue depth and operation throughput are tracked
- Go runtime metrics (goroutines, memory, GC) are exported automatically

## Approach

Pure OpenTelemetry SDK for both metrics and traces, exported via OTLP (gRPC default, HTTP option). No Prometheus client library — if scrape-based collection is needed later, the OTel Prometheus bridge can be added without changing instrumentation code.

---

## 1. Telemetry Bootstrap

### Package

New package: `internal/telemetry`

### Configuration

Three new CLI flags in `cmd/navarisd/main.go` (using Go stdlib `flag` single-dash convention):

| Flag | Default | Description |
|------|---------|-------------|
| `-otlp-endpoint` | `""` (disabled) | OTLP collector endpoint (e.g., `localhost:4317`) |
| `-otlp-protocol` | `grpc` | Transport protocol: `grpc` or `http` |
| `-service-name` | `navarisd` | Service name in telemetry data |

### Init / Shutdown

`telemetry.Init(ctx, cfg) (shutdown func(context.Context) error, err error)`

When `cfg.Endpoint` is empty, sets global OTel providers to no-op and returns a no-op shutdown. Otherwise:

1. Create OTLP exporters (gRPC or HTTP based on protocol flag) — one trace exporter via `otlptracegrpc.New()` or `otlptracehttp.New()`, and one metric exporter via `otlpmetricgrpc.New()` or `otlpmetrichttp.New()`.
2. Create `TracerProvider` with a `BatchSpanProcessor` wrapping the trace exporter. Resource attributes: `service.name`.
3. Create `MeterProvider` with a `PeriodicReader` (default 60s export interval) wrapping the metric exporter. Same resource attributes.
4. Set both as global providers via `otel.SetTracerProvider` and `otel.SetMeterProvider`.
5. Set W3C trace context propagator via `otel.SetTextMapPropagator`.
6. Register Go runtime instrumentation (must be after global providers are set): `if err := otelruntime.Start(); err != nil { return nil, fmt.Errorf("runtime instrumentation: %w", err) }`. Import as `otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"` to avoid shadowing stdlib `runtime`.
7. Return a shutdown function that flushes and stops both providers.

`main.go` calls `Init` after flag parsing. The returned `shutdown` function must be called **after** all other components have stopped — specifically, it must not be placed as a bare `defer` at the top of `run()`, as that would execute it before `disp.Stop()` returns. The existing shutdown sequence in `main.go` is `httpSrv.Shutdown()` → `gc.Stop()` → `disp.Stop()`; append `telemetry.Shutdown()` after `disp.Stop()`. This ensures all in-flight operations have completed and their final metrics/spans are recorded before the telemetry providers flush and shut down.

### Dependencies (new direct)

- `go.opentelemetry.io/otel` (includes sub-packages `propagation`, `codes`)
- `go.opentelemetry.io/otel/sdk`
- `go.opentelemetry.io/otel/metric` (separate module, will promote from indirect to direct)
- `go.opentelemetry.io/otel/trace` (separate module, will promote from indirect to direct)
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`
- `go.opentelemetry.io/contrib/instrumentation/runtime` (import as `otelruntime`)

---

## 2. HTTP Metrics Middleware

### Location

New file: `internal/api/metrics.go`

### Instruments

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `http.server.request.duration` | Histogram (seconds) | `method`, `route`, `status_code` | Request latency distribution |
| `http.server.active_requests` | UpDownCounter | `method` | In-flight request count |
| `http.server.request.size` | Histogram (bytes) | `method`, `route` | Request body size distribution |

`http.server.response.size` is intentionally omitted — response bodies are typically small JSON and tracking them adds complexity (wrapping `ResponseWriter`) with little diagnostic value. Can be added later if needed.

### Behavior

The middleware wraps the handler, recording start time and incrementing the active counter before the request, and recording duration/size and decrementing the counter after.

### Endpoint Exclusions

The `/v1/events` WebSocket endpoint and `/v1/sandboxes/{id}/exec` long-running synchronous endpoint are excluded from both metrics and tracing middleware. These long-lived connections would create misleading latency data and unbounded spans. If observability for these endpoints is needed later, dedicated instrumentation (e.g., connection count gauge, message throughput counter) should be added separately.

The `route` label uses the mux pattern (e.g., `/v1/sandboxes/{id}`), not the actual URL path, to prevent high-cardinality label explosion. Extract this from `r.Pattern` (the `Pattern` field on `*http.Request`, available since Go 1.22, populated by `http.ServeMux` with the matched pattern including method prefix — e.g., `"GET /v1/sandboxes/{id}"`; strip the method prefix to get the route).

### Registration

The middleware is inserted in the chain in `server.go` only when telemetry is enabled. A `telemetry.Enabled()` check (package-level bool, written once during `Init` before any handler is registered, read-only thereafter — no synchronization needed) gates registration. When disabled, the chain is unchanged.

Middleware position: tracing and metrics wrap the outermost chain, before `requestID`. This ensures span context is available throughout the request lifecycle, including in request ID generation and logging. Full chain when enabled: `tracing -> metrics -> requestID -> auth -> logging -> mux`.

---

## 3. HTTP Tracing Middleware

### Location

Same file: `internal/api/metrics.go`

### Behavior

Creates a span per request:
- Span name: `HTTP {METHOD} {route}` (e.g., `HTTP POST /v1/sandboxes`)
- Attributes: `http.request.method`, `http.route`, `http.response.status_code`, `url.path`
- On error (5xx): set span status to `Error`
- Inject span context into `r.Context()` so downstream code inherits it

### Trace Propagation

The W3C `traceparent` propagator (set in Init) extracts incoming trace context from request headers, enabling distributed tracing when callers (CLI, SDK) send trace headers.

### Registration

Same gating as metrics middleware. Inserted before the metrics middleware so the span wraps the entire request lifecycle. Same endpoint exclusions apply (WebSocket `/v1/events` and long-running `/v1/sandboxes/{id}/exec`).

---

## 4. Service Layer Tracing

### Location

Existing files in `internal/service/`

### Approach

Each public service method starts a child span:

```go
ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateSandbox")
defer span.End()
```

The span inherits the HTTP span's trace context via `ctx`. On error, set span status and record the error:

```go
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
}
```

Key attributes added to spans where relevant:
- `sandbox.id`, `snapshot.id`, `image.ref` — for correlation (custom attributes use dot-separated lowercase, following OTel naming conventions)
- `provider.backend` — which backend handled the operation

No metrics at this layer — HTTP metrics already capture end-to-end latency.

Note: The exec handler (`POST /v1/sandboxes/{id}/exec`) calls `SandboxService.Get()` to fetch the sandbox (producing a service span for that lookup), but then calls `Provider.Exec()` directly — there is no dedicated `service.ExecInSandbox` span. The exec endpoint is excluded from HTTP middleware (see Sections 2 and 3), so it will have no HTTP-level spans or metrics. The health endpoint (`GET /v1/health`) calls `Provider.Health()` directly without going through the service layer, but is covered by HTTP-level metrics and tracing middleware. Its provider-level span is sufficient for diagnostics.

---

## 5. Provider Metrics

### Location

New file: `internal/telemetry/provider.go` — helper for provider instrumentation.

The helpers are called from provider implementations (Firecracker, Incus).

### Instruments

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `provider.operation.duration` | Histogram (seconds) | `backend`, `operation` | Per-operation latency |
| `provider.sandbox.count` | Gauge | `backend`, `state` | Current sandbox count by state |

### Provider Spans

Each provider method creates a span using `otel.Tracer("navaris.provider")`: `provider.{operation}` (e.g., `provider.CreateSandbox`). Attributes include `backend` and resource IDs. These appear as children of the service span in traces.

### sandbox.count Gauge

Updated via callbacks: the gauge reads from the provider's in-memory VM map (Firecracker) or queries the Incus daemon via the client API (Incus). Registered once during provider init via `metric.Int64ObservableGauge` with a callback function. The `state` label values are: `running` (PID > 0 and process alive), `stopped` (PID == 0 or process dead, not stopping), `stopping` (Stopping flag set).

---

## 6. Dispatcher Metrics

### Location

Existing file: `internal/worker/dispatcher.go`

### Instruments

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `dispatcher.queue.depth` | Observable Gauge | — | Current number of queued operations |
| `dispatcher.inflight` | Observable Gauge | — | Currently executing operations |
| `dispatcher.operations.total` | Counter | `type`, `status` | Completed operations (`succeeded`/`failed`/`cancelled`) |

### Implementation

**Queue depth and inflight** are implemented as `Int64ObservableGauge` instruments with callback functions that read current state:
- `queue.depth` callback returns `len(d.queue)` (buffered channel length)
- `inflight` callback returns `len(d.sem)` (the existing semaphore channel whose capacity equals concurrency; its length is the number of acquired slots, i.e., currently executing workers)

This avoids adding new state and ensures gauges always reflect actual state, even if operations are drained or cancelled without going through normal paths.

**Operations total** is a synchronous `Int64Counter`, incremented when an operation handler returns. The `status` label uses values matching the domain types: `succeeded`, `failed`, `cancelled`.

### Out of Scope

GC worker and startup reconciler are not instrumented in this iteration. They can be added later following the same pattern if needed.

---

## Files to Create/Modify

| File | Action |
|------|--------|
| `internal/telemetry/telemetry.go` | Create — Init, Shutdown, Enabled, config types |
| `internal/telemetry/provider.go` | Create — provider instrumentation helpers |
| `internal/api/metrics.go` | Create — HTTP metrics and tracing middleware |
| `internal/api/server.go` | Modify — conditionally register telemetry middleware |
| `internal/service/*.go` | Modify — add span creation to public methods |
| `internal/worker/dispatcher.go` | Modify — add dispatcher metrics |
| `cmd/navarisd/main.go` | Modify — add flags, call telemetry.Init |
| `go.mod` | Modify — add OTel direct dependencies |
