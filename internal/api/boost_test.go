package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

func TestPostBoost_OK(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-1", domain.SandboxRunning, "mock")

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		BoostID         string `json:"boost_id"`
		SandboxID       string `json:"sandbox_id"`
		BoostedCPULimit *int   `json:"boosted_cpu_limit"`
		ExpiresAt       string `json:"expires_at"`
		State           string `json:"state"`
	}
	parseJSON(t, rec, &got)
	if got.BoostedCPULimit == nil || *got.BoostedCPULimit != 4 {
		t.Fatalf("BoostedCPULimit = %+v", got.BoostedCPULimit)
	}
	if got.State != "active" {
		t.Errorf("state = %q", got.State)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.ExpiresAt); err != nil {
		t.Errorf("expires_at not parseable: %v", err)
	}
}

func TestPostBoost_StoppedSandbox_Error(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-1", domain.SandboxStopped, "mock")

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})

	// SandboxRunning required → ErrInvalidState → 422 (existing mapping in response.go).
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestPostBoost_BothFieldsOmitted_400(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-1", domain.SandboxRunning, "mock")

	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/"+sbx.SandboxID+"/boost",
		map[string]any{"duration_seconds": 60})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPostBoost_NotFound_404(t *testing.T) {
	env := newTestEnv(t)
	rec := doRequest(t, env.handler, http.MethodPost,
		"/v1/sandboxes/missing/boost",
		map[string]any{"cpu_limit": 4, "duration_seconds": 60})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}
