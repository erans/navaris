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
// on a running sandbox. It works on both backends:
//   - Incus: cgroup memory.max is updated.
//   - Firecracker: balloon is deflated/inflated within the boot-time ceiling.
//     With the default --firecracker-mem-headroom-mult=2.0, a sandbox booted
//     with memory_limit_mb=256 has a 512 MiB ceiling, so we can grow up to 512.
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

	// Resize to a higher value still within the ceiling (256 * 2.0 = 512).
	resp, err := c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		MemoryLimitMB: ptrIntResize(384),
	})
	if err != nil {
		t.Fatalf("UpdateSandboxResources: %v", err)
	}
	if !resp.AppliedLive {
		t.Fatalf("AppliedLive=false; expected true on running sandbox")
	}
	if resp.MemoryLimitMB == nil || *resp.MemoryLimitMB != 384 {
		t.Fatalf("response MemoryLimitMB = %v; want 384", resp.MemoryLimitMB)
	}

	// Read it back via Get.
	sbx, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if sbx.MemoryLimitMB == nil || *sbx.MemoryLimitMB != 384 {
		t.Fatalf("persisted MemoryLimitMB = %v; want 384", sbx.MemoryLimitMB)
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

	// 600 > ceiling 512 → expect 409 with exceeds_ceiling.
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

// TestResize_CPU_Live_Firecracker_Rejected verifies that CPU resize on a
// running Firecracker VM is cleanly rejected with cpu_resize_unsupported_by_backend.
// Memory resize and stopped-sandbox CPU resize are unaffected.
func TestResize_CPU_Live_Firecracker_Rejected(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): Incus supports live CPU resize", img)
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

	_, err = c.UpdateSandboxResources(ctx, sandboxID, client.UpdateResourcesRequest{
		CPULimit: ptrIntResize(2),
	})
	if err == nil {
		t.Fatal("expected error on CPU resize against running Firecracker VM, got nil")
	}
	if !strings.Contains(err.Error(), "cpu_resize_unsupported_by_backend") {
		t.Fatalf("expected error to contain 'cpu_resize_unsupported_by_backend', got: %v", err)
	}
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
