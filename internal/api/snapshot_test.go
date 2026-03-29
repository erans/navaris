package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// createStoppedSandbox creates a sandbox and transitions it to stopped state
// for snapshot testing.
func createStoppedSandbox(t *testing.T, env *testEnv, projectID string) string {
	t.Helper()
	sandboxID := createTestSandbox(t, env, projectID)
	// The mock provider returns it as running after create. We need to stop it.
	op, err := env.sandboxes.Stop(context.Background(), sandboxID, false)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	_ = op
	return sandboxID
}

func TestCreateSnapshot(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/snapshots",
		map[string]any{
			"label":            "snap-1",
			"consistency_mode": "stopped",
		})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Type"] != "create_snapshot" {
		t.Fatalf("expected type create_snapshot, got %v", resp["Type"])
	}
}

func TestCreateSnapshotMissingLabel(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	rec := doRequest(t, env.handler, "POST", "/v1/sandboxes/"+sandboxID+"/snapshots",
		map[string]any{})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListSnapshots(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	// Create a snapshot
	env.snapshots.Create(context.Background(), sandboxID, "snap-1", domain.ConsistencyStopped)
	env.dispatcher.WaitIdle()

	rec := doRequest(t, env.handler, "GET", "/v1/sandboxes/"+sandboxID+"/snapshots", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	data := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(data))
	}
}

func TestGetSnapshot(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	op, err := env.snapshots.Create(context.Background(), sandboxID, "snap-1", domain.ConsistencyStopped)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	snapshotID := op.ResourceID

	rec := doRequest(t, env.handler, "GET", "/v1/snapshots/"+snapshotID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["SnapshotID"] != snapshotID {
		t.Fatalf("expected %s, got %v", snapshotID, resp["SnapshotID"])
	}
}

func TestGetSnapshotNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/snapshots/nonexistent", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRestoreSnapshot(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	op, err := env.snapshots.Create(context.Background(), sandboxID, "snap-1", domain.ConsistencyStopped)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()
	snapshotID := op.ResourceID

	rec := doRequest(t, env.handler, "POST", "/v1/snapshots/"+snapshotID+"/restore", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSnapshot(t *testing.T) {
	env := newTestEnv(t)
	projectID := createTestProject(t, env)
	sandboxID := createStoppedSandbox(t, env, projectID)

	// Directly insert a snapshot so we don't need to wait for operation
	now := time.Now().UTC()
	snap := &domain.Snapshot{
		SnapshotID:      uuid.NewString(),
		SandboxID:       sandboxID,
		Backend:         "mock",
		BackendRef:      "snap-ref",
		Label:           "to-delete",
		State:           domain.SnapshotReady,
		ConsistencyMode: domain.ConsistencyStopped,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	env.store.SnapshotStore().Create(context.Background(), snap)

	rec := doRequest(t, env.handler, "DELETE", "/v1/snapshots/"+snap.SnapshotID, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}
