//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestMixedProviderSandboxes verifies that a single navarisd instance can
// manage both Incus containers and Firecracker VMs simultaneously.
// Only runs when NAVARIS_TEST_MIXED=1 is set.
func TestMixedProviderSandboxes(t *testing.T) {
	if os.Getenv("NAVARIS_TEST_MIXED") != "1" {
		t.Skip("NAVARIS_TEST_MIXED not set")
	}

	c := newClient()
	ctx := context.Background()

	// Verify health reports both backends.
	health, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	t.Logf("health: backend=%s healthy=%v", health.Backend, health.Healthy)
	if !health.Healthy {
		t.Fatalf("expected healthy, got error: %s", health.Error)
	}

	proj := createTestProject(t, c)

	// --- Create an Incus container ---
	incusOp, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "mixed-incus",
		ImageID:   "alpine/3.21",
		Backend:   "incus",
	}, waitOpts())
	if err != nil {
		t.Fatalf("create incus sandbox: %v", err)
	}
	if incusOp.State != client.OpSucceeded {
		t.Fatalf("incus sandbox failed: state=%s error=%s", incusOp.State, incusOp.ErrorText)
	}
	incusSbxID := incusOp.ResourceID
	t.Logf("incus sandbox created: %s", incusSbxID)
	t.Cleanup(func() {
		c.DestroySandboxAndWait(context.Background(), incusSbxID, waitOpts())
	})

	// --- Create a Firecracker VM ---
	fcOp, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "mixed-firecracker",
		ImageID:   "alpine-3.21",
		Backend:   "firecracker",
	}, waitOpts())
	if err != nil {
		t.Fatalf("create firecracker sandbox: %v", err)
	}
	if fcOp.State != client.OpSucceeded {
		t.Fatalf("firecracker sandbox failed: state=%s error=%s", fcOp.State, fcOp.ErrorText)
	}
	fcSbxID := fcOp.ResourceID
	t.Logf("firecracker sandbox created: %s", fcSbxID)
	t.Cleanup(func() {
		c.DestroySandboxAndWait(context.Background(), fcSbxID, waitOpts())
	})

	// --- Verify both are running ---
	incusSbx, err := c.GetSandbox(ctx, incusSbxID)
	if err != nil {
		t.Fatalf("get incus sandbox: %v", err)
	}
	if incusSbx.State != "running" {
		t.Fatalf("incus sandbox state: %s", incusSbx.State)
	}
	if incusSbx.Backend != "incus" {
		t.Fatalf("incus sandbox backend: %s", incusSbx.Backend)
	}

	fcSbx, err := c.GetSandbox(ctx, fcSbxID)
	if err != nil {
		t.Fatalf("get firecracker sandbox: %v", err)
	}
	if fcSbx.State != "running" {
		t.Fatalf("firecracker sandbox state: %s", fcSbx.State)
	}
	if fcSbx.Backend != "firecracker" {
		t.Fatalf("firecracker sandbox backend: %s", fcSbx.Backend)
	}

	// --- Exec on both ---
	incusExec, err := c.Exec(ctx, incusSbxID, client.ExecRequest{
		Command: []string{"echo", "hello-incus"},
	})
	if err != nil {
		t.Fatalf("exec incus: %v", err)
	}
	if incusExec.ExitCode != 0 {
		t.Fatalf("incus exec exit %d: %s", incusExec.ExitCode, incusExec.Stderr)
	}
	if incusExec.Stdout != "hello-incus\n" {
		t.Fatalf("incus exec stdout: %q", incusExec.Stdout)
	}
	t.Logf("incus exec: %q", incusExec.Stdout)

	fcExec, err := c.Exec(ctx, fcSbxID, client.ExecRequest{
		Command: []string{"echo", "hello-firecracker"},
	})
	if err != nil {
		t.Fatalf("exec firecracker: %v", err)
	}
	if fcExec.ExitCode != 0 {
		t.Fatalf("firecracker exec exit %d: %s", fcExec.ExitCode, fcExec.Stderr)
	}
	if fcExec.Stdout != "hello-firecracker\n" {
		t.Fatalf("firecracker exec stdout: %q", fcExec.Stdout)
	}
	t.Logf("firecracker exec: %q", fcExec.Stdout)

	t.Log("mixed-provider test passed: both Incus and Firecracker sandboxes running and executable")
}
