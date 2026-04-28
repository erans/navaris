//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// TestSandbox_HonorsRequestedCPULimit creates a Firecracker sandbox with
// cpu_limit=1 (and ceiling=2 via --firecracker-vcpu-headroom-mult=2.0
// in the FC compose) and asserts that the host cgroup's CPU bandwidth
// limit is enforced by measuring the wall-clock ratio of two parallel
// CPU-bound awk loops vs one serial awk. With ceiling=2 and limit=1,
// two threads share 1 host CPU, so the parallel run takes ~2x the serial
// run; we assert the ratio >= 1.5 (theoretical 2.0, with margin for
// scheduler noise).
//
// Reading /sys/fs/cgroup/cpu.max from inside the guest does not work:
// the host cgroup is invisible to the guest kernel, which has its own
// independent cgroup hierarchy. Workload-based detection is the only
// in-guest way to observe the host throttle.
//
// Skipped on Incus: this test exercises the FC cgroup CPU bandwidth
// path. Incus has its own (separate) cpu enforcement mechanism.
func TestSandbox_HonorsRequestedCPULimit(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s); FC-specific cgroup throttle test", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 1
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "limits-cpu-1",
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

	n := calibrateAwk(t, c, sandboxID)

	tSerial := runAwk(t, c, sandboxID, n)
	tParallel := runAwkParallel(t, c, sandboxID, n, 2)
	ratio := float64(tParallel) / float64(tSerial)

	t.Logf("cpu_limit=1, ceiling=2: serial=%s parallel=%s ratio=%.2f",
		time.Duration(tSerial), time.Duration(tParallel), ratio)

	const minThrottledRatio = 1.5
	if ratio < minThrottledRatio {
		t.Fatalf("parallel/serial ratio = %.2f, want >= %.2f (cgroup CPU bandwidth not enforced)",
			ratio, minThrottledRatio)
	}
}
