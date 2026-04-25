//go:build integration

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// TestFork_ChildrenSeeParentFiles_AndDivergeIndependently is the headline
// stage-2 test: write a sentinel inside the parent, fork x3, verify all
// three children see the sentinel and each can diverge.
//
// Skipped unless /srv/firecracker is on a reflink-capable filesystem
// (btrfs, XFS w/ reflink, or bcachefs). Without CoW, the test would still
// be correct logically but would take seconds per fork — acceptable for a
// nightly run, but skipped in default integration to keep the suite fast.
func TestFork_ChildrenSeeParentFiles_AndDivergeIndependently(t *testing.T) {
	if !reflinkCapableHost(t) {
		t.Skip("test requires /srv/firecracker to be on a reflink-capable filesystem")
	}

	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	parentID := createTestSandbox(t, c, proj.ProjectID, "fork-parent")

	const sentinel = "/root/sentinel"
	exec1, err := c.Exec(ctx, parentID, client.ExecRequest{
		Command: []string{"sh", "-c", "echo PARENT > " + sentinel},
	})
	if err != nil {
		t.Fatalf("exec write sentinel: %v", err)
	}
	if exec1.ExitCode != 0 {
		t.Fatalf("exec write sentinel exit %d: %s", exec1.ExitCode, exec1.Stderr)
	}

	// Fork x3.
	forkOp, err := c.Fork(ctx, parentID, 3)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Wait for the fork operation to complete.
	if _, err := c.WaitForOperation(ctx, forkOp.OperationID, nil); err != nil {
		t.Fatalf("wait fork op: %v", err)
	}

	// List sandboxes in the project; the 3 children are tagged with
	// metadata.fork_parent_id == parentID.
	all, err := c.ListSandboxes(ctx, proj.ProjectID, "")
	if err != nil {
		t.Fatalf("list sandboxes: %v", err)
	}
	var children []client.Sandbox
	for _, s := range all {
		if s.SandboxID == parentID {
			continue
		}
		fp, _ := s.Metadata["fork_parent_id"].(string)
		if fp == parentID {
			children = append(children, s)
		}
	}
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}

	// Each child sees the sentinel.
	for _, child := range children {
		child := child // capture for closure
		t.Cleanup(func() { _, _ = c.DestroySandbox(ctx, child.SandboxID) })
		// Children are created in pending; wait for running before exec.
		if err := waitForState(ctx, c, child.SandboxID, "running"); err != nil {
			t.Fatalf("child %s never reached running: %v", child.SandboxID, err)
		}
		out, err := c.Exec(ctx, child.SandboxID, client.ExecRequest{
			Command: []string{"cat", sentinel},
		})
		if err != nil {
			t.Fatalf("child %s read sentinel: %v", child.SandboxID, err)
		}
		if got := strings.TrimSpace(out.Stdout); got != "PARENT" {
			t.Errorf("child %s sentinel = %q, want PARENT", child.SandboxID, got)
		}
	}

	// Each child writes a unique tag; siblings and parent stay isolated.
	for i, child := range children {
		tag := "CHILD-" + string(rune('A'+i))
		exec2, err := c.Exec(ctx, child.SandboxID, client.ExecRequest{
			Command: []string{"sh", "-c", "echo " + tag + " > " + sentinel},
		})
		if err != nil {
			t.Fatalf("child %s write tag: %v", child.SandboxID, err)
		}
		if exec2.ExitCode != 0 {
			t.Fatalf("child %s write tag exit %d", child.SandboxID, exec2.ExitCode)
		}
	}

	// Parent unaffected.
	parentReadOut, err := c.Exec(ctx, parentID, client.ExecRequest{
		Command: []string{"cat", sentinel},
	})
	if err != nil {
		t.Fatalf("parent read sentinel after child writes: %v", err)
	}
	if got := strings.TrimSpace(parentReadOut.Stdout); got != "PARENT" {
		t.Errorf("parent sentinel changed after child writes: got %q", got)
	}

	// Each child reads back its own tag.
	for i, child := range children {
		expected := "CHILD-" + string(rune('A'+i))
		out, err := c.Exec(ctx, child.SandboxID, client.ExecRequest{
			Command: []string{"cat", sentinel},
		})
		if err != nil {
			t.Fatalf("child %s reread sentinel: %v", child.SandboxID, err)
		}
		if got := strings.TrimSpace(out.Stdout); got != expected {
			t.Errorf("child %d sentinel = %q, want %q", i, got, expected)
		}
	}
}

// reflinkCapableHost reports whether /srv/firecracker is on a filesystem
// that supports reflink (btrfs, XFS with reflink=1, bcachefs).
func reflinkCapableHost(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("findmnt", "-n", "-o", "FSTYPE", "/srv/firecracker").CombinedOutput()
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(out)) {
	case "btrfs", "xfs", "bcachefs":
		return true
	}
	return false
}

// waitForState polls until the sandbox transitions to the given state, or
// the context is cancelled or a 60s deadline elapses.
func waitForState(ctx context.Context, c *client.Client, sandboxID, want string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		s, err := c.GetSandbox(ctx, sandboxID)
		if err == nil && string(s.State) == want {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("waitForState %s: %w", want, err)
			}
			return fmt.Errorf("waitForState %s: timeout (last state %v)", want, s.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
