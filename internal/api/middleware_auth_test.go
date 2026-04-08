package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/webui"
)

// authTestEnv wraps a protected handler with the production middleware so
// each table-driven case exercises the real code path.
func authTestEnv(t *testing.T, token string, sessionKey []byte) http.Handler {
	t.Helper()
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	return api.AuthMiddlewareForTest(token, sessionKey)(protected)
}

func signedCookie(t *testing.T, key []byte) *http.Cookie {
	t.Helper()
	val, err := webui.NewSigner(key).Sign(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: webui.CookieName, Value: val}
}

func TestAuthMiddlewareCookieAloneAllowsSafeMethod(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	req.AddCookie(signedCookie(t, key))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareBearerAlonePasses(t *testing.T) {
	h := authTestEnv(t, "tok", nil)
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuthMiddlewareBearerWinsOverCookie(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "tok", key)
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (bearer wins and fails)", rec.Code)
	}
}

func TestAuthMiddlewareCookieMismatchedOrigin(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAuthMiddlewareCookieMissingOriginMismatchedReferer(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Referer", "https://evil.example/page")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAuthMiddlewareCookieMissingOriginAndReferer(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAuthMiddlewareCookieMatchingOrigin(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://navaris.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareSafeMethodMismatchedOriginAllowed(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (safe methods skip Origin check)", rec.Code)
	}
}

func TestAuthMiddlewareNoCookieNoBearerNoConfig(t *testing.T) {
	h := authTestEnv(t, "", nil) // both empty → test-mode fallthrough
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareNoAuthWithTokenConfigured(t *testing.T) {
	h := authTestEnv(t, "tok", nil)
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuthMiddlewareCookieMatchingOriginIgnoresPortDifference(t *testing.T) {
	// Production topology: navarisd listens on :8080 behind a reverse proxy
	// that terminates TLS and forwards with Host: 127.0.0.1:8080 but the
	// browser sets Origin: https://navaris.example (no port).
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://navaris.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		// Hostnames still don't match (127.0.0.1 != navaris.example) →
		// this test documents that we only strip ports, we don't magically
		// trust arbitrary hostnames.
		t.Fatalf("status = %d, want 403 (hostnames differ even though ports are stripped)", rec.Code)
	}
}

func TestAuthMiddlewareCookieHostHasPortOriginDoesNot(t *testing.T) {
	// Same-hostname, different port shape: r.Host includes :8080 while
	// Origin omits the port. Must compare by hostname.
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example:8080"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://navaris.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (hostnames match when ports stripped)", rec.Code)
	}
}

func TestAuthMiddlewareCookieOriginHasPortHostDoesNot(t *testing.T) {
	// Mirror of the above: r.Host is bare, Origin has a port.
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "navaris.example"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://navaris.example:8443")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (hostnames match when ports stripped)", rec.Code)
	}
}

func TestAuthMiddlewareCookieHostCaseInsensitive(t *testing.T) {
	// url.Parse lowercases u.Hostname() per RFC 3986; r.Host is not
	// normalised. Compare case-insensitively on both sides.
	key := []byte("k")
	h := authTestEnv(t, "", key)
	req := httptest.NewRequest("POST", "/v1/sandboxes", nil)
	req.Host = "NAVARIS.EXAMPLE"
	req.AddCookie(signedCookie(t, key))
	req.Header.Set("Origin", "https://navaris.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (host compare must be case-insensitive)", rec.Code)
	}
}

func TestAuthMiddlewareQueryTokenFallbackOnWebSocketEndpoint(t *testing.T) {
	h := authTestEnv(t, "tok", nil)
	req := httptest.NewRequest("GET", "/v1/events?token=tok", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareQueryTokenFallbackOnAttachEndpoint(t *testing.T) {
	h := authTestEnv(t, "tok", nil)
	req := httptest.NewRequest("GET", "/v1/sandboxes/abc/attach?token=tok", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareQueryTokenNotAcceptedOnRegularEndpoint(t *testing.T) {
	h := authTestEnv(t, "tok", nil)
	req := httptest.NewRequest("GET", "/v1/sandboxes?token=tok", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (query token only accepted on WS routes)", rec.Code)
	}
}

func TestAuthMiddlewareQueryTokenIgnoredWhenCookiePresent(t *testing.T) {
	key := []byte("k")
	h := authTestEnv(t, "tok", key)
	req := httptest.NewRequest("GET", "/v1/events?token=wrong", nil)
	req.AddCookie(signedCookie(t, key))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (cookie should win)", rec.Code)
	}
}
