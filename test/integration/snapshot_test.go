//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestSnapshotRestoreToSandbox(t *testing.T) {
	if os.Getenv("NAVARIS_SKIP_SNAPSHOTS") == "1" {
		t.Skip("snapshots not supported by this backend")
	}
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "snap-restore-sbx")

	// Write a marker file into the sandbox.
	execResp, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo marker-before > /tmp/marker.txt"},
	})
	if err != nil {
		t.Fatalf("exec write marker: %v", err)
	}
	if execResp.ExitCode != 0 {
		t.Fatalf("exec write marker exit %d: %s", execResp.ExitCode, execResp.Stderr)
	}

	// Stop and snapshot.
	stopOp, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopOp.State != client.OpSucceeded {
		t.Fatalf("stop failed: state=%s error=%s", stopOp.State, stopOp.ErrorText)
	}

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "restore-test-snap",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait snapshot: %v", err)
	}
	if snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot failed: state=%s error=%s", snapOp.State, snapOp.ErrorText)
	}
	snapshotID := snapOp.ResourceID
	t.Cleanup(func() {
		delOp, _ := c.DeleteSnapshot(context.Background(), snapshotID)
		if delOp != nil {
			c.WaitForOperation(context.Background(), delOp.OperationID, waitOpts())
		}
	})

	// Start sandbox again and modify the marker.
	startOp, err := c.StartSandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if startOp.State != client.OpSucceeded {
		t.Fatalf("start failed: state=%s error=%s", startOp.State, startOp.ErrorText)
	}

	execResp, err = c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo marker-after > /tmp/marker.txt"},
	})
	if err != nil {
		t.Fatalf("exec modify marker: %v", err)
	}
	if execResp.ExitCode != 0 {
		t.Fatalf("exec modify marker exit %d: %s", execResp.ExitCode, execResp.Stderr)
	}

	// Stop and restore the snapshot.
	stopOp, err = c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil {
		t.Fatalf("stop before restore: %v", err)
	}
	if stopOp.State != client.OpSucceeded {
		t.Fatalf("stop before restore failed: state=%s error=%s", stopOp.State, stopOp.ErrorText)
	}

	restoreOp, err := c.RestoreSnapshot(ctx, snapshotID)
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	restoreOp, err = c.WaitForOperation(ctx, restoreOp.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait restore: %v", err)
	}
	if restoreOp.State != client.OpSucceeded {
		t.Fatalf("restore failed: state=%s error=%s", restoreOp.State, restoreOp.ErrorText)
	}

	// Start and verify marker is back to original value.
	startOp, err = c.StartSandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil {
		t.Fatalf("start after restore: %v", err)
	}
	if startOp.State != client.OpSucceeded {
		t.Fatalf("start after restore failed: state=%s error=%s", startOp.State, startOp.ErrorText)
	}

	execResp, err = c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"cat", "/tmp/marker.txt"},
	})
	if err != nil {
		t.Fatalf("exec read marker: %v", err)
	}
	if execResp.Stdout != "marker-before\n" {
		t.Fatalf("expected marker-before after restore, got %q", execResp.Stdout)
	}
	t.Log("snapshot restore verified: marker file reverted to pre-snapshot state")
}
