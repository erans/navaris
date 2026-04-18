package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/testutil/apiserver"
)

// bearerInjector is a RoundTripper that adds an Authorization: Bearer header
// to every outbound request, letting the MCP SDK client authenticate against
// the bearer-only handler.
type bearerInjector struct{ token string }

func (b bearerInjector) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestHTTPHandler_RejectsCookieAuth confirms that a navaris_ui_session cookie
// without a Bearer header is rejected with 401. The handler never inspects
// cookies — it only checks Authorization — so any non-bearer request fails here
// before ever reaching the backing API server. Passing a dummy apiURL is fine
// because auth short-circuits before the handler talks to navarisd.
func TestHTTPHandler_RejectsCookieAuth(t *testing.T) {
	handler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
		LocalAPIURL: "http://127.0.0.1:0", // irrelevant — auth short-circuits first
		AuthToken:   "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.AddCookie(&http.Cookie{Name: "navaris_ui_session", Value: "some-session-value"})
	// No Authorization: Bearer header — cookie auth must not grant MCP access.

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header on 401 response")
	}
}

// TestHTTPHandler_AcceptsBearerToken verifies that a valid bearer token reaches
// the MCP server and that the tools list is non-empty — confirming the full
// auth→handler→NewServer path works end-to-end over HTTP.
func TestHTTPHandler_AcceptsBearerToken(t *testing.T) {
	apiURL, _, _ := apiserver.New(t)
	const token = "test-token"

	handler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
		LocalAPIURL: apiURL,
		AuthToken:   token,
	})

	httpSrv := httptest.NewServer(handler)
	t.Cleanup(httpSrv.Close)

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-bearer"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: httpSrv.URL,
		HTTPClient: &http.Client{
			Transport: bearerInjector{token: token},
		},
	}

	sess, err := mc.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected at least one tool, got none")
	}
}
