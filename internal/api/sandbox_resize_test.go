package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// ensureProject creates a project named "resize-test" and returns its ID.
// It is called per-test (each test gets its own env with a fresh DB).
func ensureProject(t *testing.T, env *testEnv) string {
	t.Helper()
	p, err := env.projects.Create(context.Background(), "resize-test", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ProjectID
}

// seedSandbox directly inserts a sandbox row in a given state into the store,
// bypassing the async operation pipeline so tests can start in a known state.
func seedSandbox(t *testing.T, env *testEnv, projectID string, name string, state domain.SandboxState, backend string) *domain.Sandbox {
	t.Helper()
	cpu, mem := 1, 256
	sbx := &domain.Sandbox{
		SandboxID:     "sbx-" + uuid.NewString()[:8],
		ProjectID:     projectID,
		Name:          name,
		State:         state,
		Backend:       backend,
		BackendRef:    "ref-" + uuid.NewString()[:8],
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
		NetworkMode:   domain.NetworkIsolated,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := env.store.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

func TestPatchResources_OK(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-ok", domain.SandboxStopped, "incus")

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{"cpu_limit": 4, "memory_limit_mb": 1024})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		SandboxID     string `json:"sandbox_id"`
		CPULimit      int    `json:"cpu_limit"`
		MemoryLimitMB int    `json:"memory_limit_mb"`
		AppliedLive   bool   `json:"applied_live"`
	}
	parseJSON(t, rec, &got)
	if got.SandboxID != sbx.SandboxID {
		t.Fatalf("sandbox_id = %q, want %q", got.SandboxID, sbx.SandboxID)
	}
	if got.CPULimit != 4 {
		t.Fatalf("cpu_limit = %d, want 4", got.CPULimit)
	}
	if got.MemoryLimitMB != 1024 {
		t.Fatalf("memory_limit_mb = %d, want 1024", got.MemoryLimitMB)
	}
	if got.AppliedLive {
		t.Fatal("applied_live = true, want false for stopped sandbox")
	}
}

func TestPatchResources_BothFieldsOmitted_400(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-empty", domain.SandboxStopped, "mock")

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

func TestPatchResources_NotFound_404(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/nonexistent-id/resources",
		map[string]any{"cpu_limit": 2})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

func TestPatchResources_ProviderResizeError_409(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-running", domain.SandboxRunning, "mock")

	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonExceedsCeiling,
			Detail: "requested 8 vCPUs but ceiling is 4",
		}
	}

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{"cpu_limit": 8})

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, domain.ResizeReasonExceedsCeiling) {
		t.Fatalf("response body %q does not contain reason %q", body, domain.ResizeReasonExceedsCeiling)
	}
}

func TestPatchResources_BoundsViolation_400(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-bounds", domain.SandboxStopped, "firecracker")

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{"cpu_limit": 99})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cpu_limit") {
		t.Fatalf("response body %q does not mention cpu_limit", body)
	}
}

func TestPatchResources_CgroupUnavailable_503(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-cgroup-503", domain.SandboxRunning, "mock")

	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCgroupUnavailable,
			Detail: "private-cgroup-path/sys/fs/cgroup/scrub-me",
		}
	}

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{"cpu_limit": 2})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	// The reason must survive into the body so callers can distinguish this
	// non-transient failure from a generic overload-style 503.
	body := rec.Body.String()
	if !strings.Contains(body, domain.ResizeReasonCgroupUnavailable) {
		t.Errorf("body %q missing reason %q", body, domain.ResizeReasonCgroupUnavailable)
	}
	// Detail can include host paths or raw I/O strings — must not appear
	// in the response body for 5xx (defense-in-depth against backend errors
	// that leak internal state).
	if strings.Contains(body, "private-cgroup-path") {
		t.Errorf("body leaks Detail %q: %s", "private-cgroup-path", body)
	}
	// Retry-After:1 would advertise transient retry; cgroup-unavailable is
	// not transient (only restart fixes it). It must not be set here.
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want empty for non-transient resize failure", got)
	}
}

func TestPatchResources_CgroupWriteFailed_500(t *testing.T) {
	env := newTestEnv(t)
	projID := ensureProject(t, env)
	sbx := seedSandbox(t, env, projID, "sbx-cgroup-500", domain.SandboxRunning, "mock")

	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{
			Reason: domain.ResizeReasonCgroupWriteFailed,
			Detail: "open /sys/fs/cgroup/secret/cpu.max: input/output error",
		}
	}

	rec := doRequest(t, env.handler, http.MethodPatch,
		"/v1/sandboxes/"+sbx.SandboxID+"/resources",
		map[string]any{"cpu_limit": 2})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, domain.ResizeReasonCgroupWriteFailed) {
		t.Errorf("body %q missing reason %q", body, domain.ResizeReasonCgroupWriteFailed)
	}
	// Detail must be scrubbed on 5xx (defense-in-depth against backend
	// errors that leak host paths or raw I/O strings).
	if strings.Contains(body, "input/output error") || strings.Contains(body, "/sys/fs/cgroup/secret") {
		t.Errorf("body leaks Detail: %s", body)
	}
}
