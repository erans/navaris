package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/service"
)

func TestCreatePort(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/ports",
		map[string]any{"target_port": 8080})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["TargetPort"] != float64(8080) {
		t.Fatalf("expected target_port 8080, got %v", resp["TargetPort"])
	}
	if resp["PublishedPort"] == nil {
		t.Fatal("expected PublishedPort to be set")
	}
}

func TestCreatePortInvalidTarget(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/ports",
		map[string]any{"target_port": 0})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListPorts(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	// Create two port bindings via API
	doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/ports",
		map[string]any{"target_port": 8080})
	doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/ports",
		map[string]any{"target_port": 9090})

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes/"+sandboxID+"/ports", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(data))
	}
}

func TestDeletePort(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	// Create a port first
	doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/ports",
		map[string]any{"target_port": 8080})

	rec := doRequest(t, env.handler, "DELETE", "/v1/sandboxes/"+sandboxID+"/ports/8080", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify port is gone
	listRec := doRequest(t, env.handler, "GET", "/v1/sandboxes/"+sandboxID+"/ports", nil)
	var resp map[string]any
	parseJSON(t, listRec, &resp)
	data := resp["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("expected 0 ports after delete, got %d", len(data))
	}
}

func TestCreatePortSandboxNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/nonexistent/ports",
		map[string]any{"target_port": 8080})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStartSandbox(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	// Create and stop a sandbox so we can test start
	op, err := env.sandboxes.Create(context.Background(), projectID, "start-test", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	sandboxID := op.ResourceID

	// Stop it first
	_, err = env.sandboxes.Stop(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/start", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}
