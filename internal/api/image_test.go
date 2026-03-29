package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestRegisterImage(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/images/register", map[string]any{
		"name":        "ubuntu",
		"version":     "22.04",
		"backend":     "incus",
		"backend_ref": "img-ref-1",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Name"] != "ubuntu" {
		t.Fatalf("expected ubuntu, got %v", resp["Name"])
	}
	if resp["State"] != "ready" {
		t.Fatalf("expected state ready, got %v", resp["State"])
	}
}

func TestRegisterImageMissingFields(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "POST", "/v1/images/register", map[string]any{
		"name": "ubuntu",
		// Missing version, backend, backend_ref
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPromoteImage(t *testing.T) {
	env := newTestEnv(t)

	// Create a ready snapshot to promote
	now := time.Now().UTC()
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)
	snap := &domain.Snapshot{
		SnapshotID:      uuid.NewString(),
		SandboxID:       sandboxID,
		Backend:         "mock",
		BackendRef:      "snap-ref",
		Label:           "promotable",
		State:           domain.SnapshotReady,
		ConsistencyMode: domain.ConsistencyStopped,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	env.store.SnapshotStore().Create(context.Background(), snap)

	rec := doRequest(t, env.handler, "POST", "/v1/images", map[string]any{
		"snapshot_id": snap.SnapshotID,
		"name":        "promoted-img",
		"version":     "1.0",
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Type"] != "promote_snapshot" {
		t.Fatalf("expected promote_snapshot, got %v", resp["Type"])
	}
}

func TestListImages(t *testing.T) {
	env := newTestEnv(t)

	// Register two images
	env.images.Register(context.Background(), "img-a", "1.0", "incus", "ref-a", "amd64")
	env.images.Register(context.Background(), "img-b", "2.0", "incus", "ref-b", "arm64")

	rec := doRequest(t, env.handler, "GET", "/v1/images", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 images, got %d", len(data))
	}
}

func TestGetImage(t *testing.T) {
	env := newTestEnv(t)

	img, err := env.images.Register(context.Background(), "ubuntu", "22.04", "incus", "ref-1", "amd64")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "GET", "/v1/images/"+img.ImageID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["ImageID"] != img.ImageID {
		t.Fatalf("expected %s, got %v", img.ImageID, resp["ImageID"])
	}
}

func TestGetImageNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/images/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteImage(t *testing.T) {
	env := newTestEnv(t)

	img, err := env.images.Register(context.Background(), "to-delete", "1.0", "incus", "ref-del", "amd64")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, env.handler, "DELETE", "/v1/images/"+img.ImageID, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}
