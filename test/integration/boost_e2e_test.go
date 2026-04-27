//go:build integration

package integration

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

func ptrIntE2E(v int) *int { return &v }

// readMemAvailableKB reads MemAvailable (kB) from /proc/meminfo inside the
// sandbox. Used by the FC boost test to verify that the balloon actually
// inflated/deflated as the boost transitioned through the lifecycle.
func readMemAvailableKB(t *testing.T, c *client.Client, sandboxID string) int {
	t.Helper()
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "awk '/^MemAvailable:/ {print $2}' /proc/meminfo"},
	})
	if err != nil {
		t.Fatalf("exec MemAvailable: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec MemAvailable exit %d: stderr=%s", exec.ExitCode, exec.Stderr)
	}
	kb, err := strconv.Atoi(strings.TrimSpace(exec.Stdout))
	if err != nil {
		t.Fatalf("parse MemAvailable %q: %v", exec.Stdout, err)
	}
	return kb
}

// readNproc returns the value of `nproc` inside the sandbox. Used by the
// Incus CPU boost test — Incus enforces cpu_limit by setting limits.cpu on
// the container, which causes /sys/devices/system/cpu/online (and therefore
// nproc) to reflect the limit.
func readNproc(t *testing.T, c *client.Client, sandboxID string) int {
	t.Helper()
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"nproc"},
	})
	if err != nil {
		t.Fatalf("exec nproc: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec nproc exit %d: stderr=%s", exec.ExitCode, exec.Stderr)
	}
	n, err := strconv.Atoi(strings.TrimSpace(exec.Stdout))
	if err != nil {
		t.Fatalf("parse nproc %q: %v", exec.Stdout, err)
	}
	return n
}

// TestBoost_E2E_FC_Memory_VisibleInGuest creates a Firecracker sandbox with
// 512 MiB, then issues a SHRINK boost to 320 MiB and verifies — by reading
// /proc/meminfo from inside the guest — that MemAvailable actually drops
// once the balloon inflates, and recovers after the boost is cancelled.
//
// FC live memory resize uses a virtio-balloon device: shrinking the limit
// inflates the balloon (pages allocated inside the guest are returned to
// the host), so the guest kernel sees less available memory. Growing
// memory above the boot ceiling requires --firecracker-mem-headroom-mult
// > 1.0, which is not enabled in CI; the shrink path exercises the full
// live-update plumbing without needing headroom.
//
// Skipped on Incus because Incus's test image lacks lxcfs — /proc/meminfo
// inside the container reports host RAM, not the cgroup limit, so the
// in-guest verification doesn't apply. (See TestSandbox_HonorsRequestedMemoryLimit
// for the same reason.)
func TestBoost_E2E_FC_Memory_VisibleInGuest(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): /proc/meminfo doesn't reflect cgroup limits without lxcfs", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "boost-e2e-mem",
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

	memBeforeKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable before boost: %d kB (~%d MiB)", memBeforeKB, memBeforeKB/1024)

	// Shrink to 320 MiB → balloon inflates by ~192 MiB.
	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntE2E(320),
		DurationSeconds: 30,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	// The balloon driver inflates asynchronously; give it a few seconds.
	time.Sleep(4 * time.Second)

	memDuringKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable during boost: %d kB (~%d MiB)", memDuringKB, memDuringKB/1024)

	// Expect at least ~100 MiB drop. Boost is 192 MiB; we leave headroom for
	// kernel allocations, balloon-driver overhead, and inflate-in-flight pages.
	const minDropKB = 100 * 1024
	if memBeforeKB-memDuringKB < minDropKB {
		t.Errorf("expected MemAvailable to drop by at least %d kB after boost; before=%d kB, during=%d kB (delta=%d kB)",
			minDropKB, memBeforeKB, memDuringKB, memBeforeKB-memDuringKB)
	}

	// Cancel and verify recovery.
	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}

	// Deflate is also asynchronous.
	time.Sleep(4 * time.Second)

	memAfterKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable after cancel: %d kB (~%d MiB)", memAfterKB, memAfterKB/1024)

	// After deflate, MemAvailable should recover most of the way back. We
	// allow a 50 MiB slack — the kernel's working set may have legitimately
	// grown during the test, and deflate isn't always 1:1 with the inflated
	// pages on every kernel.
	const recoveryFloorKB = 50 * 1024
	if memBeforeKB-memAfterKB > recoveryFloorKB {
		t.Errorf("MemAvailable did not recover after cancel; before=%d kB, after=%d kB (deficit=%d kB)",
			memBeforeKB, memAfterKB, memBeforeKB-memAfterKB)
	}
}

// TestBoost_E2E_Incus_CPU_VisibleInGuest creates an Incus container with
// cpu_limit=1, then boosts to cpu_limit=2 and verifies — by running `nproc`
// inside the container — that the container's effective CPU count actually
// changes live, and reverts after the boost is cancelled.
//
// Incus enforces cpu_limit by writing limits.cpu on the instance config,
// which Incus translates to a cpuset that the container sees as the number
// of online CPUs. nproc reads /sys/devices/system/cpu/online (or the
// cgroup CPU controller view), so the change is visible from inside.
//
// No memory limit is set at create time — limits.memory at create-time
// triggers `forkstart exit status 1` on the docker-in-docker CI runner
// (see test/integration/boost_test.go's skip comments). Boosting CPU only
// avoids that issue while still exercising the live-update path end-to-end.
//
// Skipped on Firecracker because FC rejects CPU resize on running VMs
// (cpu_resize_unsupported_by_backend in internal/provider/firecracker/sandbox_resize.go).
func TestBoost_E2E_Incus_CPU_VisibleInGuest(t *testing.T) {
	img := baseImage()
	if !strings.Contains(img, "/") {
		t.Skipf("skipping on Firecracker (image=%s): FC rejects CPU resize on running VMs (cpu_resize_unsupported_by_backend)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu := 1
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "boost-e2e-cpu",
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

	if got := readNproc(t, c, sandboxID); got != 1 {
		t.Fatalf("baseline nproc = %d, want 1", got)
	}

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		CPULimit:        ptrIntE2E(2),
		DurationSeconds: 30,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	// limits.cpu propagates through the cgroup write near-instantly, but
	// give the container a moment for the cpuset to settle on the kernel
	// view that nproc reads.
	time.Sleep(1 * time.Second)

	if got := readNproc(t, c, sandboxID); got != 2 {
		t.Errorf("nproc during boost = %d, want 2", got)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}

	time.Sleep(1 * time.Second)

	if got := readNproc(t, c, sandboxID); got != 1 {
		t.Errorf("nproc after cancel = %d, want 1", got)
	}
}
