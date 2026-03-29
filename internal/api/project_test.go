package api_test

import (
	"net/http"
	"testing"
)

func TestCreateProject(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/projects",
		map[string]any{"name": "my-project"})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Name"] != "my-project" {
		t.Fatalf("expected name my-project, got %v", resp["Name"])
	}
	if resp["ProjectID"] == nil || resp["ProjectID"] == "" {
		t.Fatal("expected ProjectID to be set")
	}
}

func TestCreateProjectMissingName(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/projects",
		map[string]any{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListProjects(t *testing.T) {
	env := newTestEnv(t)

	// Create two projects
	doRequest(t, env.handler, "POST", "/v1/projects", map[string]any{"name": "proj-1"})
	doRequest(t, env.handler, "POST", "/v1/projects", map[string]any{"name": "proj-2"})

	rec := doRequest(t, env.handler, "GET", "/v1/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(data))
	}
}

func TestGetProject(t *testing.T) {
	env := newTestEnv(t)

	// Create
	createRec := doRequest(t, env.handler, "POST", "/v1/projects",
		map[string]any{"name": "get-test"})
	var created map[string]any
	parseJSON(t, createRec, &created)
	id := created["ProjectID"].(string)

	// Get
	rec := doRequest(t, env.handler, "GET", "/v1/projects/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["ProjectID"] != id {
		t.Fatalf("expected project ID %s, got %v", id, resp["ProjectID"])
	}
}

func TestGetProjectNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/projects/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateProject(t *testing.T) {
	env := newTestEnv(t)

	createRec := doRequest(t, env.handler, "POST", "/v1/projects",
		map[string]any{"name": "old-name"})
	var created map[string]any
	parseJSON(t, createRec, &created)
	id := created["ProjectID"].(string)

	rec := doRequest(t, env.handler, "PUT", "/v1/projects/"+id,
		map[string]any{"name": "new-name"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Name"] != "new-name" {
		t.Fatalf("expected new-name, got %v", resp["Name"])
	}
}

func TestDeleteProject(t *testing.T) {
	env := newTestEnv(t)

	createRec := doRequest(t, env.handler, "POST", "/v1/projects",
		map[string]any{"name": "delete-me"})
	var created map[string]any
	parseJSON(t, createRec, &created)
	id := created["ProjectID"].(string)

	rec := doRequest(t, env.handler, "DELETE", "/v1/projects/"+id, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify gone
	getRec := doRequest(t, env.handler, "GET", "/v1/projects/"+id, nil)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getRec.Code)
	}
}
