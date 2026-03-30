# Observability: Metrics and Tracing

## Goal

Add OpenTelemetry-based metrics and distributed tracing to navarisd, exported via OTLP. When enabled, operators get request latency histograms, sandbox/operation counts, dispatcher queue depth, and end-to-end traces through the API, service, and provider layers. When disabled (no endpoint configured), behavior is unchanged — zero overhead.

## Success Criteria

- `--otlp-endpoint` flag enables telemetry export; omitting it keeps current behavior
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

Three new CLI flags in `cmd/navarisd/main.go`:

| Flag | Default | Description |
|------|---------|-------------|
| `--otlp-endpoint` | `""` (disabled) | OTLP collector endpoint (e.g., `localhost:4317`) |
| `--otlp-protocol` | `grpc` | Transport protocol: `grpc` or `http` |
| `--service-name` | `navarisd` | Service name in telemetry data |

### Init / Shutdown

`telemetry.Init(ctx, cfg) (shutdown func(context.Context) error, err error)`

When `cfg.Endpoint` is empty, sets global OTel providers to no-op and returns a no-op shutdown. Otherwise:

1. Create OTLP exporter (gRPC or HTTP based on protocol flag).
2. Create `TracerProvider` with a `BatchSpanProcessor` wrapping the exporter. Resource attributes: `service.name`, `service.version`.
3. Create `MeterProvider` with a `PeriodicReader` (default 30s interval) wrapping the exporter. Same resource attributes.
4. Register Go runtime instrumentation via `go.opentelemetry.io/contrib/instrumentation/runtime`.
5. Set both as global providers via `otel.SetTracerProvider` and `otel.SetMeterProvider`.
6. Set W3C trace context propagator via `otel.SetTextMapPropagator`.
7. Return a shutdown function that flushes and stops both providers.

`main.go` calls `Init` after flag parsing and defers `shutdown`.

### Dependencies (new direct)

- `go.opentelemetry.io/otel`
- `go.opentelemetry.io/otel/sdk`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc`
- `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp`
- `go.opentelemetry.io/contrib/instrumentation/runtime`
- `go.opentelemetry.io/otel/propagation`

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

### Behavior

The middleware wraps the handler, recording start time and incrementing the active counter before the request, and recording duration/size and decrementing the counter after.

The `route` label uses the mux pattern (e.g., `/v1/sandboxes/{id}`), not the actual URL path, to prevent high-cardinality label explosion. Extract this from the request's `http.ServeMux` pattern (Go 1.22+ stores it in the request via `r.Pattern`).

### Registration

The middleware is inserted in the chain in `server.go` only when telemetry is enabled. A `telemetry.Enabled()` check (package-level bool set during Init) gates registration. When disabled, the chain is unchanged.

---

## 3. HTTP Tracing Middleware

### Location

Same file: `internal/api/metrics.go` (or `tracing.go` if it gets long)

### Behavior

Creates a span per request:
- Span name: `HTTP {METHOD} {route}` (e.g., `HTTP POST /v1/sandboxes`)
- Attributes: `http.request.method`, `http.route`, `http.response.status_code`, `url.path`
- On error (5xx): set span status to `Error`
- Inject span context into `r.Context()` so downstream code inherits it

### Trace Propagation

The W3C `traceparent` propagator (set in Init) extracts incoming trace context from request headers, enabling distributed tracing when callers (CLI, SDK) send trace headers.

### Registration

Same gating as metrics middleware. Inserted before the metrics middleware so the span wraps the entire request lifecycle.

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
- `sandbox.id`, `snapshot.id`, `image.ref` — for correlation
- `provider.backend` — which backend handled the operation

No metrics at this layer — HTTP metrics already capture end-to-end latency.

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

Each provider method creates a span: `provider.{operation}` (e.g., `provider.CreateSandbox`). Attributes include `backend` and resource IDs. These appear as children of the service span in traces.

### sandbox.count Gauge

Updated via callbacks: the gauge reads from the provider's in-memory VM map (Firecracker) or queries the store (Incus). Registered once during provider init via `metric.Int64ObservableGauge` with a callback function.

---

## 6. Dispatcher Metrics

### Location

Existing file: `internal/worker/dispatcher.go`

### Instruments

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `dispatcher.queue.depth` | Gauge | — | Current number of queued operations |
| `dispatcher.inflight` | Gauge | — | Currently executing operations |
| `dispatcher.operations.total` | Counter | `type`, `status` | Completed operations (completed/failed/cancelled) |

### Recording Points

- **Enqueue**: increment queue depth
- **Dequeue** (worker picks up): decrement queue depth, increment inflight
- **Complete** (handler returns): decrement inflight, increment operations total with type and status labels

The Meter is obtained from the global provider. When telemetry is disabled, the global no-op meter makes all recording calls zero-cost.

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
