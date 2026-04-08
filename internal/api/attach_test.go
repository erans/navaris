package api_test

import (
	"net/http/httptest"
	"testing"
)

func TestAttachReturns404ForUnknownSandbox(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest("GET", "/v1/sandboxes/sbx_missing/attach", nil)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAttachReturns409ForStoppedSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)
	// Stop it so State != Running.
	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/stop", nil)
	if rec.Code/100 != 2 {
		t.Fatalf("stop: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	env.dispatcher.WaitIdle()

	req := httptest.NewRequest("GET", "/v1/sandboxes/"+sandboxID+"/attach", nil)
	rec = httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}
}
