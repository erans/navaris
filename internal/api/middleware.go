package api

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/webui"
)

type contextKey string

const requestIDKey contextKey = "request_id"

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authMiddleware gates /v1/* with either a Bearer token or a signed
// session cookie. When both token and sessionKey are empty, the middleware
// is a pass-through (existing test-mode behaviour).
//
// Bearer always wins when an Authorization header is present — a programmatic
// client explicitly sending a bearer shouldn't have its request cookie-checked.
//
// On cookie auth with an unsafe method (POST/PUT/DELETE/PATCH), the middleware
// additionally verifies Origin (or Referer fallback) matches Host. Mismatch
// returns 403, not 401, to distinguish "authenticated but refused" from
// "not authenticated".
func authMiddleware(token string, sessionKey []byte) func(http.Handler) http.Handler {
	var signer *webui.Signer
	if len(sessionKey) > 0 {
		signer = webui.NewSigner(sessionKey)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Test-mode fallthrough: no auth configured at all.
			if token == "" && signer == nil {
				next.ServeHTTP(w, r)
				return
			}

			// 1. Bearer wins.
			if bearer := extractBearerToken(r); bearer != "" {
				if token == "" || bearer != token {
					respondError(w, domain.ErrUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// 2. Cookie fallback.
			if signer != nil {
				if c, err := r.Cookie(webui.CookieName); err == nil && c.Value != "" {
					if _, _, err := signer.Verify(c.Value); err != nil {
						respondError(w, domain.ErrUnauthorized)
						return
					}
					if isUnsafeMethod(r.Method) && !originMatchesHost(r) {
						respondError(w, domain.ErrForbidden)
						return
					}
					next.ServeHTTP(w, r)
					return
				}
			}

			// 2b. WebSocket query-param fallback (/v1/events, /v1/sandboxes/{id}/attach).
			if token != "" && isWebSocketRoute(r.URL.Path) {
				if q := r.URL.Query().Get("token"); q != "" {
					if q != token {
						respondError(w, domain.ErrUnauthorized)
						return
					}
					next.ServeHTTP(w, r)
					return
				}
			}

			// 3. Nothing → 401.
			respondError(w, domain.ErrUnauthorized)
		})
	}
}

// AuthMiddlewareForTest exposes the internal auth middleware to tests in
// package api_test.
func AuthMiddlewareForTest(token string, sessionKey []byte) func(http.Handler) http.Handler {
	return authMiddleware(token, sessionKey)
}

func extractBearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func originMatchesHost(r *http.Request) bool {
	host := r.Host
	if origin := r.Header.Get("Origin"); origin != "" {
		return sameHost(origin, host)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return sameHost(referer, host)
	}
	return false
}

// sameHost reports whether the hostname in rawURL matches the hostname in
// host, ignoring ports and case. Ports are stripped because r.Host typically
// carries the internal listen address (e.g. 127.0.0.1:8080) while Origin
// carries the public-facing hostname from the browser (e.g. navaris.example).
func sameHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), hostnameOnly(host))
}

// hostnameOnly strips an optional ":port" suffix from host. It returns the
// whole input unchanged if there is no port or if SplitHostPort cannot parse
// it (which happens for bare hostnames without a port).
func hostnameOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

type statusCapture struct {
	http.ResponseWriter
	code int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.code = code
	sc.ResponseWriter.WriteHeader(code)
}

// Hijack implements http.Hijacker so that WebSocket upgrades work when the
// underlying ResponseWriter supports hijacking (e.g. http.Server connections).
func (sc *statusCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := sc.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("http.ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}

// isWebSocketRoute reports whether a path is a WebSocket endpoint that
// accepts a ?token= query parameter as an auth fallback for CLI clients
// that can't set headers on a handshake. Browsers always use the cookie
// path and never append this query parameter.
func isWebSocketRoute(path string) bool {
	if path == "/v1/events" {
		return true
	}
	// /v1/sandboxes/{id}/attach
	if strings.HasPrefix(path, "/v1/sandboxes/") && strings.HasSuffix(path, "/attach") {
		return true
	}
	return false
}

func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sc := &statusCapture{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sc, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sc.code,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromContext(r.Context()),
			)
		})
	}
}
