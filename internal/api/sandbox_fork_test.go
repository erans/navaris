package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/service"
)

func TestForkSandbox_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	op, err := env.sandboxes.Create(context.Background(), projectID, "parent", "img-1", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	env.dispatcher.WaitIdle()
	parentID := op.ResourceID

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+parentID+"/fork", map[string]any{
		"count": 2,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestForkSandbox_RejectsCountLessThan1(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	op, _ := env.sandboxes.Create(context.Background(), projectID, "parent", "img-1", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()
	parentID := op.ResourceID

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+parentID+"/fork", map[string]any{
		"count": 0,
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestForkSandbox_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/does-not-exist/fork", map[string]any{
		"count": 1,
	})
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
