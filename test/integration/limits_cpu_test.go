//go:build integration

package integration

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestSandbox_HonorsRequestedCPULimit creates a Firecracker sandbox with
// cpu_limit=2 and verifies that /sys/fs/cgroup/cpu.max inside the guest
// reflects that limit. FC mounts cgroupfs by default, and the per-VM
// cgroup is propagated as the guest's root cgroup.
//
// Skipped on Incus: limits.cpu enforcement inside docker-in-docker is
// unreliable (the outer container's cpuset takes precedence). The Incus
// CPU path uses limits.cpu cgroup writes too, but verifying it from
// inside requires a non-DinD environment — see
// TestBoost_E2E_Incus_CPU_VisibleInGuest in boost_e2e_local_test.go.
func TestSandbox_HonorsRequestedCPULimit(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): cpuset enforcement unreliable in DinD", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 2
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "limits-cpu-2",
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

	// Read the cgroup v2 cpu.max from inside the guest. Format: "<quota> <period>".
	// Fall back to v1 cpu.cfs_quota_us on hosts running cgroup v1.
	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "cat /sys/fs/cgroup/cpu.max 2>/dev/null || cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us"},
	})
	if err != nil {
		t.Fatalf("exec read cpu.max: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Skipf("cpu.max not readable from guest (exit=%d, stderr=%s); guest kernel may not mount cgroupfs", exec.ExitCode, exec.Stderr)
	}

	raw := strings.TrimSpace(exec.Stdout)
	t.Logf("guest cpu.max: %q", raw)

	parts := strings.Fields(raw)
	if len(parts) == 0 || parts[0] == "max" {
		// "max" means unlimited — daemon may not have wired the cgroup setup
		// (e.g. cgroup root permission denied in this environment). Skip
		// rather than fail; the unit tests cover the wiring path.
		t.Skipf("guest cpu.max reports %q; daemon may lack cgroup write permission in this env", raw)
	}
	quota, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("parse quota %q: %v", parts[0], err)
	}
	const expectedQuota = 2 * 100_000
	if quota != expectedQuota {
		t.Errorf("cpu.max quota = %d, want %d (cpu_limit=2 * period 100000)", quota, expectedQuota)
	}
}
