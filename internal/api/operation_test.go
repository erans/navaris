package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/service"
)

func TestGetOperation(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	op, err := env.sandboxes.Create(context.Background(), projectID, "op-test", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "GET", "/v1/operations/"+op.OperationID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["OperationID"] != op.OperationID {
		t.Fatalf("expected %s, got %v", op.OperationID, resp["OperationID"])
	}
}

func TestGetOperationNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/operations/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListOperations(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	op, err := env.sandboxes.Create(context.Background(), projectID, "list-op-test", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	rec := doRequest(t, env.handler, "GET", "/v1/operations?sandbox_id="+op.SandboxID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) < 1 {
		t.Fatalf("expected at least 1 operation, got %d", len(data))
	}
}

func TestCancelOperation(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	op, err := env.sandboxes.Create(context.Background(), projectID, "cancel-test", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "POST", "/v1/operations/"+op.OperationID+"/cancel", nil)
	// Cancel returns 204 regardless
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}
