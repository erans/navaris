package webui_test

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/navaris/navaris/internal/webui"
)

func mockFS() fs.FS {
	return fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html>INDEX")},
		"assets/app.js":   {Data: []byte("console.log('app');")},
		"assets/style.css": {Data: []byte("body{}")},
	}
}

func TestAssetHandlerServesIndexAtRoot(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "INDEX") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html*", ct)
	}
}

func TestAssetHandlerServesStaticAsset(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestAssetHandlerDeepLinkFallsBackToIndex(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/sandboxes/abc/terminal", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "INDEX") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestAssetHandlerRefusesV1Paths(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/v1/sandboxes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAssetHandlerRefusesUIPaths(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/ui/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAssetHandlerNilFSReturns404(t *testing.T) {
	h := webui.NewAssetHandler(nil)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAssetHandlerUnknownAssetIs404(t *testing.T) {
	h := webui.NewAssetHandler(mockFS())
	req := httptest.NewRequest("GET", "/assets/missing.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Missing real files under /assets/ are 404, not fallback.
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
