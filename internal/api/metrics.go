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

			methodAttr := metric.WithAttributes(attribute.String("method", method))
			activeReqs.Add(r.Context(), 1, methodAttr)
			defer activeReqs.Add(r.Context(), -1, methodAttr)

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
