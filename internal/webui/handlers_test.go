package webui_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/webui"
)

func newTestConfig(t *testing.T) webui.Config {
	t.Helper()
	return webui.Config{
		Password:   "hunter2",
		SessionKey: []byte("test-key-do-not-ship"),
		SessionTTL: time.Hour,
		LoginDelay: 0, // tests skip the 200ms real delay
	}
}

func TestLoginSuccessSetsCookie(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	body, _ := json.Marshal(map[string]string{"password": "hunter2"})
	req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "navaris_ui_session" {
		t.Fatalf("cookie missing: %+v", cookies)
	}
	if cookies[0].Value == "" {
		t.Fatal("cookie value empty")
	}
	if !cookies[0].HttpOnly {
		t.Fatal("cookie should be HttpOnly")
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookies[0].SameSite)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	body, _ := json.Marshal(map[string]string{"password": "wrong"})
	req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no cookie should be set on failure")
	}
}

func TestLoginRateLimit(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.RateCapacity = 3
	cfg.RateRefill = 3
	cfg.RateInterval = time.Minute
	h := webui.NewHandlers(cfg)
	body, _ := json.Marshal(map[string]string{"password": "wrong"})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader(body))
		req.RemoteAddr = "10.0.0.1:1"
		rec := httptest.NewRecorder()
		h.Login(rec, req)
		if rec.Code != 401 {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1"
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != 429 {
		t.Fatalf("4th attempt: status = %d, want 429", rec.Code)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	cfg := newTestConfig(t)
	h := webui.NewHandlers(cfg)
	signed, err := webui.NewSigner(cfg.SessionKey).Sign(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/ui/logout", nil)
	req.AddCookie(&http.Cookie{Name: "navaris_ui_session", Value: signed})
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("expected Max-Age=-1 cookie, got %+v", cookies)
	}
}

func TestLogoutWithoutCookie(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	req := httptest.NewRequest("POST", "/ui/logout", nil)
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (logout requires cookie per spec)", rec.Code)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no cookie should be set when logout fails")
	}
}

func TestLogoutWithInvalidCookie(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	req := httptest.NewRequest("POST", "/ui/logout", nil)
	req.AddCookie(&http.Cookie{Name: "navaris_ui_session", Value: "not-a-valid-cookie"})
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (invalid cookie is not a valid session)", rec.Code)
	}
}

func TestMeWithoutCookie(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	req := httptest.NewRequest("GET", "/ui/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Fatalf("body = %s, want authenticated:false", rec.Body.String())
	}
}

func TestMeWithValidCookie(t *testing.T) {
	cfg := newTestConfig(t)
	h := webui.NewHandlers(cfg)
	signed, err := webui.NewSigner(cfg.SessionKey).Sign(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/ui/me", nil)
	req.AddCookie(&http.Cookie{Name: "navaris_ui_session", Value: signed})
	rec := httptest.NewRecorder()
	h.Me(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("body = %s, want authenticated:true", rec.Body.String())
	}
}

func TestLoginFixedDelay(t *testing.T) {
	cfg := newTestConfig(t)
	cfg.LoginDelay = 50 * time.Millisecond
	h := webui.NewHandlers(cfg)
	start := time.Now()
	body, _ := json.Marshal(map[string]string{"password": "wrong"})
	req := httptest.NewRequest("POST", "/ui/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("login returned in %v, want at least 50ms (fixed delay)", elapsed)
	}
}

func TestNotAllowedUnknownPath(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	req := httptest.NewRequest("GET", "/ui/foo", nil)
	rec := httptest.NewRecorder()
	h.NotAllowed(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestNotAllowedWrongMethodOnKnownPath(t *testing.T) {
	h := webui.NewHandlers(newTestConfig(t))
	req := httptest.NewRequest("GET", "/ui/login", nil)
	rec := httptest.NewRecorder()
	h.NotAllowed(rec, req)
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "POST" {
		t.Fatalf("Allow header = %q, want POST", allow)
	}
}
