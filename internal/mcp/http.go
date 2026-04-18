package mcp

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

// HTTPOptions configures the bearer-only HTTP handler for the embedded MCP endpoint.
type HTTPOptions struct {
	// LocalAPIURL is navarisd's base URL (typically "http://localhost:<port>").
	LocalAPIURL string

	// AuthToken is the bearer token inbound requests must present.
	// When empty, the handler accepts all requests without checking auth —
	// suitable only when the listener is already restricted to localhost.
	AuthToken string

	// ReadOnly hides mutating tools when true.
	ReadOnly bool

	// MaxTimeout caps any per-tool timeout_seconds argument.
	MaxTimeout time.Duration
}

// NewHTTPHandler returns an http.Handler that serves the embedded MCP endpoint.
//
// The handler accepts only Bearer-token auth. Requests that carry a
// navaris_ui_session cookie (or any other non-bearer credential) are rejected
// with 401 — the handler never inspects cookies, it simply requires a valid
// Authorization: Bearer header. This prevents a web-UI session from
// accidentally granting MCP access through CSRF.
//
// The underlying StreamableHTTPHandler is allocated once (not per-request) so
// that it can maintain session state across the multiple HTTP round-trips that
// the streamable MCP protocol requires (e.g. the POST that initialises a
// session followed by the GET that opens the SSE stream). The bearer token is
// read from the incoming request inside the per-session factory, so each new
// MCP session still gets its own navarisd client scoped to the caller's token.
func NewHTTPHandler(opts HTTPOptions) http.Handler {
	// mcpHandler is stateful: it maps session IDs to live MCP sessions.
	// Creating it once here (rather than per HTTP request) lets follow-up
	// requests (e.g. the SSE GET after the initialising POST) find their
	// session by ID.
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(req *http.Request) *mcpsdk.Server {
		bearer := extractBearer(req)
		c := client.NewClient(client.WithURL(opts.LocalAPIURL), client.WithToken(bearer))
		return NewServer(Options{
			Client:     c,
			ReadOnly:   opts.ReadOnly,
			MaxTimeout: opts.MaxTimeout,
		})
	}, nil)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := extractBearer(r)

		// Reject anything without a valid bearer when auth is enabled.
		// subtle.ConstantTimeCompare avoids timing-based token guessing;
		// it returns 0 both for length mismatch and content mismatch, so
		// a single check covers both cases.
		if opts.AuthToken != "" {
			match := subtle.ConstantTimeCompare([]byte(bearer), []byte(opts.AuthToken))
			if bearer == "" || match != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="navaris-mcp"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		mcpHandler.ServeHTTP(w, r)
	})
}

// extractBearer pulls the token from an Authorization: Bearer <token> header.
// Returns "" when the header is absent or uses a different scheme.
// Scheme matching is case-sensitive ("Bearer "), consistent with the existing
// internal/api extractBearerToken implementation.
func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}
