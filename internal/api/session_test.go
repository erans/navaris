package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestCreateSession(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/sessions",
		map[string]any{"shell": "/bin/bash"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["SessionID"] == nil || resp["SessionID"] == "" {
		t.Fatal("expected SessionID in response")
	}
	if resp["State"] != "active" {
		t.Fatalf("expected state active, got %v", resp["State"])
	}
}

func TestCreateSessionSandboxNotRunning(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)

	// Create a sandbox but stop it
	op, err := env.sandboxes.Create(context.Background(), projectID, "stopped-sbx", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	sandboxID := op.ResourceID

	// Stop the sandbox
	_, err = env.sandboxes.Stop(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/sessions",
		map[string]any{"shell": "/bin/bash"})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSessions(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	// Create two sessions
	env.sessions.Create(context.Background(), sandboxID, domain.SessionBackingDirect, "/bin/bash")
	env.sessions.Create(context.Background(), sandboxID, domain.SessionBackingDirect, "/bin/sh")

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes/"+sandboxID+"/sessions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(data))
	}
}

func TestGetSession(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	sess, err := env.sessions.Create(context.Background(), sandboxID, domain.SessionBackingDirect, "/bin/bash")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "GET", "/v1/sessions/"+sess.SessionID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["SessionID"] != sess.SessionID {
		t.Fatalf("expected %s, got %v", sess.SessionID, resp["SessionID"])
	}
}

func TestGetSessionNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/sessions/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSession(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createTestSandbox(t, env, projectID)

	sess, err := env.sessions.Create(context.Background(), sandboxID, domain.SessionBackingDirect, "/bin/bash")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "DELETE", "/v1/sessions/"+sess.SessionID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's destroyed
	s, _ := env.sessions.Get(context.Background(), sess.SessionID)
	if s.State != domain.SessionDestroyed {
		t.Fatalf("expected destroyed state, got %s", s.State)
	}
}
