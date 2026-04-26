//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

func ptrIntBoost(v int) *int { return &v }

// TestBoost_Memory_AppliesAndReverts creates a sandbox, boosts memory by
// shrinking it (works on both backends regardless of headroom configuration),
// then waits for the timer to fire and verifies the boost is gone via GET.
//
// The 512 MiB starting limit and 384 MiB shrink target are deliberately
// generous: 256 MiB is the Firecracker minimum and works fine there, but
// Incus's CI environment seems to fail forkstart on containers below ~512
// MiB on the docker-in-docker runner. Picking a value comfortably above
// both backends' minimums keeps the test green on both legs.
func TestBoost_Memory_AppliesAndReverts(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-mem-revert",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// SHRINK boost (works regardless of headroom): persisted limit is 512;
	// boost to 384 for 3s.
	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(384),
		DurationSeconds: 3,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	// Confirm the boost shows up.
	b, err := c.GetBoost(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetBoost: %v", err)
	}
	if b.State != "active" {
		t.Errorf("state = %s", b.State)
	}

	// Wait past expiry + small slack.
	time.Sleep(5 * time.Second)

	// GetBoost should now be 404.
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatalf("expected ErrNotFound after expiry, got nil")
	}
}

func TestBoost_Cancel_RevertsImmediately(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-cancel",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(384),
		DurationSeconds: 600, // long; we'll cancel
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected 404 after cancel")
	}
}

func TestBoost_Stop_CancelsBoost(t *testing.T) {
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "boost-stop",
		ImageID: baseImage(), MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntBoost(384),
		DurationSeconds: 600,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	if _, err := c.StopSandboxAndWait(ctx, sandboxID, client.StopSandboxRequest{}, waitOpts()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected boost gone after sandbox stop")
	}
	_ = strings.Contains
}
