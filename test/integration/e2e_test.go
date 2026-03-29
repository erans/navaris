//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// TestEndToEndLifecycle exercises the full sandbox lifecycle through the SDK:
//
//  1. Create a project
//  2. Create a sandbox from a base image
//  3. Wait for sandbox to be running
//  4. Exec a command and verify stdout
//  5. Stop the sandbox and create a snapshot
//  6. Create a new sandbox from the snapshot
//  7. Destroy everything
//
// Requires a running navarisd with Incus backend.
// Run with: go test -tags integration ./test/integration/ -v
func TestEndToEndLifecycle(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	// Verify API is reachable.
	health, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("health check failed (is navarisd running?): %v", err)
	}
	if !health.Healthy {
		t.Fatalf("backend unhealthy: %s", health.Error)
	}
	t.Logf("backend: %s, healthy: %v, latency: %dms", health.Backend, health.Healthy, health.LatencyMS)

	// --- Step 1: Create project ---
	projName := "e2e-test-" + time.Now().Format("20060102-150405")
	proj, err := c.CreateProject(ctx, client.CreateProjectRequest{
		Name: projName,
		Metadata: map[string]any{
			"purpose": "integration-test",
		},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Logf("created project %s (%s)", proj.Name, proj.ProjectID)

	// Clean up project at the end.
	defer func() {
		if err := c.DeleteProject(ctx, proj.ProjectID); err != nil {
			t.Logf("warning: failed to delete project: %v", err)
		}
	}()

	// --- Step 2: Create sandbox from base image ---
	imgID := baseImage()
	createOp, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "e2e-sandbox-1",
		ImageID:   imgID,
	}, waitOpts())
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	if createOp.State != client.OpSucceeded {
		t.Fatalf("create sandbox operation failed: state=%s error=%s", createOp.State, createOp.ErrorText)
	}
	sandboxID := createOp.ResourceID
	t.Logf("created sandbox %s", sandboxID)

	// Clean up sandbox at the end (best-effort).
	defer func() {
		_, _ = c.DestroySandboxAndWait(ctx, sandboxID, waitOpts())
	}()

	// --- Step 3: Verify sandbox is running ---
	sbx, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if sbx.State != "running" {
		t.Fatalf("expected sandbox state running, got %s", sbx.State)
	}
	t.Logf("sandbox %s is running", sandboxID)

	// --- Step 4: Exec a command and verify stdout ---
	execResp, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"echo", "hello-navaris"},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if execResp.ExitCode != 0 {
		t.Fatalf("exec exit code: %d, stderr: %s", execResp.ExitCode, execResp.Stderr)
	}
	expected := "hello-navaris\n"
	if execResp.Stdout != expected {
		t.Fatalf("exec stdout: got %q, want %q", execResp.Stdout, expected)
	}
	t.Logf("exec returned: %q", execResp.Stdout)

	// --- Step 5: Stop sandbox and create snapshot ---
	stopOp, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{Force: false}, waitOpts())
	if err != nil {
		t.Fatalf("stop sandbox: %v", err)
	}
	if stopOp.State != client.OpSucceeded {
		t.Fatalf("stop operation failed: state=%s error=%s", stopOp.State, stopOp.ErrorText)
	}
	t.Logf("sandbox stopped")

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "e2e-snap",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait for snapshot operation: %v", err)
	}
	if snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot operation failed: state=%s error=%s", snapOp.State, snapOp.ErrorText)
	}
	snapshotID := snapOp.ResourceID
	t.Logf("created snapshot %s", snapshotID)

	// Clean up snapshot at the end (best-effort).
	defer func() {
		delOp, err := c.DeleteSnapshot(ctx, snapshotID)
		if err != nil {
			t.Logf("warning: failed to delete snapshot: %v", err)
			return
		}
		_, _ = c.WaitForOperation(ctx, delOp.OperationID, waitOpts())
	}()

	// --- Step 6: Create new sandbox from snapshot ---
	fromSnapOp, err := c.CreateSandboxFromSnapshot(ctx, client.CreateSandboxFromSnapshotRequest{
		ProjectID:  proj.ProjectID,
		Name:       "e2e-sandbox-from-snap",
		SnapshotID: snapshotID,
	})
	if err != nil {
		t.Fatalf("create sandbox from snapshot: %v", err)
	}
	fromSnapOp, err = c.WaitForOperation(ctx, fromSnapOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait for from-snapshot operation: %v", err)
	}
	if fromSnapOp.State != client.OpSucceeded {
		t.Fatalf("from-snapshot operation failed: state=%s error=%s", fromSnapOp.State, fromSnapOp.ErrorText)
	}
	sandbox2ID := fromSnapOp.ResourceID
	t.Logf("created sandbox from snapshot %s", sandbox2ID)

	sbx2, err := c.GetSandbox(ctx, sandbox2ID)
	if err != nil {
		t.Fatalf("get sandbox 2: %v", err)
	}
	if sbx2.State != "running" {
		t.Fatalf("expected sandbox-from-snapshot to be running, got %s", sbx2.State)
	}
	t.Logf("sandbox-from-snapshot %s is running", sandbox2ID)

	// --- Step 7: Destroy everything ---
	// Stop sandbox2 first — Incus won't delete a running instance.
	stopOp2, err := c.StopSandboxAndWait(ctx, sandbox2ID, client.StopSandboxRequest{Force: true}, waitOpts())
	if err != nil {
		t.Fatalf("stop sandbox 2: %v", err)
	}
	if stopOp2.State != client.OpSucceeded {
		t.Fatalf("stop sandbox 2 failed: state=%s error=%s", stopOp2.State, stopOp2.ErrorText)
	}

	destroyOp2, err := c.DestroySandboxAndWait(ctx, sandbox2ID, waitOpts())
	if err != nil {
		t.Fatalf("destroy sandbox 2: %v", err)
	}
	if destroyOp2.State != client.OpSucceeded {
		t.Fatalf("destroy sandbox 2 failed: state=%s error=%s", destroyOp2.State, destroyOp2.ErrorText)
	}
	t.Logf("destroyed sandbox-from-snapshot %s", sandbox2ID)

	destroyOp1, err := c.DestroySandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil {
		t.Fatalf("destroy sandbox 1: %v", err)
	}
	if destroyOp1.State != client.OpSucceeded {
		t.Fatalf("destroy sandbox 1 failed: state=%s error=%s", destroyOp1.State, destroyOp1.ErrorText)
	}
	t.Logf("destroyed sandbox 1 %s", sandboxID)

	t.Log("end-to-end lifecycle test passed")
}
