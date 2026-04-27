//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func ptrIntResize(v int) *int { return &v }

// TestResize_Memory_Live verifies that a runtime memory resize takes effect
// on a running sandbox. Tests SHRINK (which works regardless of headroom
// configuration). GROW above the original limit requires --firecracker-mem-headroom-mult > 1.0
// and is exercised in TestResize_Memory_AboveCeiling_Firecracker.
func TestResize_Memory_Live(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): /proc/meminfo from inside Incus reflects the host without lxcfs; the resize still applies but isn't observable from /proc/meminfo here", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "resize-mem-live",
		ImageID:       img,
		MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// SHRINK 256 -> 192 (works regardless of headroom multiplier).
	resp, err := c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		MemoryLimitMB: ptrIntResize(192),
	})
	if err != nil {
		t.Fatalf("UpdateSandboxResources: %v", err)
	}
	if !resp.AppliedLive {
		t.Fatalf("AppliedLive=false; expected true on running sandbox")
	}
	if resp.MemoryLimitMB == nil || *resp.MemoryLimitMB != 192 {
		t.Fatalf("response MemoryLimitMB = %v; want 192", resp.MemoryLimitMB)
	}

	// Read it back via Get.
	sbx, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if sbx.MemoryLimitMB == nil || *sbx.MemoryLimitMB != 192 {
		t.Fatalf("persisted MemoryLimitMB = %v; want 192", sbx.MemoryLimitMB)
	}
}

// TestResize_Memory_AboveCeiling_Firecracker verifies that growing memory
// past the boot-time ceiling fails with HTTP 409 (Conflict) carrying the
// reason `exceeds_ceiling`. Only meaningful on Firecracker (Incus has no
// hard ceiling). The image-prefix heuristic is the same one used by other
// per-backend tests in the file.
func TestResize_Memory_AboveCeiling_Firecracker(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): no boot-time memory ceiling on Incus", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "resize-mem-ceiling",
		ImageID:       img,
		MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// 600 > ceiling (ceiling = limit = 256 when --firecracker-mem-headroom-mult=1.0, the default) → expect 409 with exceeds_ceiling.
	_, err = c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		MemoryLimitMB: ptrIntResize(600),
	})
	if err == nil {
		t.Fatal("expected error growing past ceiling, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds_ceiling") {
		t.Fatalf("expected error to contain 'exceeds_ceiling', got: %v", err)
	}
}

// TestResize_CPU_Live_Firecracker verifies that CPU resize on a running
// Firecracker VM is enforced via cgroup CPU bandwidth (cpu.max). With
// default headroom (1.0), boot ceiling = limit, so any grow is rejected
// with exceeds_ceiling. With --firecracker-vcpu-headroom-mult > 1.0
// (production deployments and the headroom CI compose), grow up to the
// ceiling succeeds.
//
// We test the default-headroom rejection path here since CI's standard
// FC compose doesn't set headroom (the local headroom env exists for
// grow tests; see boost_e2e_local_test.go). Either result is correct
// behavior — we just no longer return cpu_resize_unsupported_by_backend.
func TestResize_CPU_Live_Firecracker(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): Incus supports live CPU resize via limits.cpu", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu, mem := 1, 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "resize-cpu-fc",
		ImageID:       img,
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	// Try to grow CPU. Without headroom this fails with exceeds_ceiling;
	// with headroom it succeeds. Both outcomes prove the path is wired.
	_, err = c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		CPULimit: ptrIntResize(2),
	})
	if err != nil {
		// Default headroom path: must be exceeds_ceiling, not the old
		// cpu_resize_unsupported_by_backend.
		if !strings.Contains(err.Error(), "exceeds_ceiling") {
			t.Fatalf("expected exceeds_ceiling, got: %v", err)
		}
		if strings.Contains(err.Error(), "cpu_resize_unsupported_by_backend") {
			t.Fatalf("CPU resize should not return cpu_resize_unsupported_by_backend anymore: %v", err)
		}
	}
	// If err is nil, we're on a headroom-enabled daemon and the resize
	// succeeded — also acceptable (no further assertion needed since the
	// in-guest verification lives in TestBoost_E2E_FC_CPU_AppliesToGuest).
}

// TestResize_CPU_Live_Incus verifies live CPU resize on an Incus container.
func TestResize_CPU_Live_Incus(t *testing.T) {
	img := baseImage()
	if !strings.Contains(img, "/") {
		t.Skipf("skipping on Firecracker (image=%s): live CPU resize not supported by FC SDK in this build", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 1
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "resize-cpu-incus",
		ImageID:   img,
		CPULimit:  &cpu,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	resp, err := c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		CPULimit: ptrIntResize(2),
	})
	if err != nil {
		t.Fatalf("UpdateSandboxResources: %v", err)
	}
	if !resp.AppliedLive {
		t.Fatal("AppliedLive=false; want true on running Incus container")
	}
	if resp.CPULimit == nil || *resp.CPULimit != 2 {
		t.Fatalf("response CPULimit = %v; want 2", resp.CPULimit)
	}
}
