package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/service"
)

// createTestProject creates a project and returns its ID.
func createTestProject(t *testing.T, env *testEnv) string {
	t.Helper()
	p, err := env.projects.Create(context.Background(), "test-project", nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.ProjectID
}

// createTestSandbox creates a sandbox via the API and waits for the operation.
// Returns the sandbox ID extracted from the operation.
func createTestSandbox(t *testing.T, env *testEnv, projectID string) string {
	t.Helper()
	op, err := env.sandboxes.Create(context.Background(), projectID, "test-sandbox", "image-1", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	return op.ResourceID
}

func TestCreateSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes", map[string]any{
		"project_id": projectID,
		"name":       "my-sandbox",
		"image_id":   "image-1",
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["OperationID"] == nil || resp["OperationID"] == "" {
		t.Fatal("expected OperationID in response")
	}
	if resp["Type"] != "create_sandbox" {
		t.Fatalf("expected type create_sandbox, got %v", resp["Type"])
	}
}

func TestCreateSandboxMissingProjectID(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes", map[string]any{
		"name":     "my-sandbox",
		"image_id": "image-1",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandboxFromSnapshot(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/from-snapshot", map[string]any{
		"project_id":  projectID,
		"name":        "snap-sandbox",
		"snapshot_id": "snap-123",
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandboxFromImage(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/from-image", map[string]any{
		"project_id": projectID,
		"name":       "img-sandbox",
		"image_id":   "img-1",
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSandboxes(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	// Create two sandboxes
	env.sandboxes.Create(context.Background(), projectID, "sbx-1", "img", service.CreateSandboxOpts{})
	env.sandboxes.Create(context.Background(), projectID, "sbx-2", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes?project_id="+projectID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d", len(data))
	}
}

func TestListSandboxesMissingProjectID(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes/"+sandboxID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["SandboxID"] != sandboxID {
		t.Fatalf("expected sandbox ID %s, got %v", sandboxID, resp["SandboxID"])
	}
}

func TestGetSandboxNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStopSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/stop",
		map[string]any{"force": false})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDestroySandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/destroy", nil)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandbox_RejectsCPUOutOfBounds(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes", map[string]any{
		"project_id": projectID,
		"name":       "bad-cpu",
		"image_id":   "img-1",
		"cpu_limit":  33,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSandboxFromSnapshot_RejectsMemoryLimit(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/from-snapshot", map[string]any{
		"project_id":      projectID,
		"name":            "bad-snap",
		"snapshot_id":     "snap-1",
		"memory_limit_mb": 1024,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
