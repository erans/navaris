//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestSnapshotRestoreToSandbox(t *testing.T) {
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
	if err != nil || stopOp.State != client.OpSucceeded {
		t.Fatalf("stop: err=%v state=%s", err, stopOp.State)
	}

	snapOp, err := c.CreateSnapshot(ctx, sandboxID, client.CreateSnapshotRequest{
		Label:           "restore-test-snap",
		ConsistencyMode: "stopped",
	})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	snapOp, err = c.WaitForOperation(ctx, snapOp.OperationID, waitOpts())
	if err != nil || snapOp.State != client.OpSucceeded {
		t.Fatalf("snapshot: err=%v state=%s", err, snapOp.State)
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
	if err != nil || startOp.State != client.OpSucceeded {
		t.Fatalf("start: err=%v state=%s", err, startOp.State)
	}

	execResp, err = c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo marker-after > /tmp/marker.txt"},
	})
	if err != nil || execResp.ExitCode != 0 {
		t.Fatalf("exec modify marker: err=%v exit=%d", err, execResp.ExitCode)
	}

	// Stop and restore the snapshot.
	stopOp, err = c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts())
	if err != nil || stopOp.State != client.OpSucceeded {
		t.Fatalf("stop before restore: err=%v state=%s", err, stopOp.State)
	}

	restoreOp, err := c.RestoreSnapshot(ctx, snapshotID)
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	restoreOp, err = c.WaitForOperation(ctx, restoreOp.OperationID, waitOpts())
	if err != nil || restoreOp.State != client.OpSucceeded {
		t.Fatalf("restore: err=%v state=%s", err, restoreOp.State)
	}

	// Start and verify marker is back to original value.
	startOp, err = c.StartSandboxAndWait(ctx, sandboxID, waitOpts())
	if err != nil || startOp.State != client.OpSucceeded {
		t.Fatalf("start after restore: err=%v state=%s", err, startOp.State)
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
