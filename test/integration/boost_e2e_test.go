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

// TestBoost_E2E_FC_BoostFromInside_VisibleInGuest creates a Firecracker
// sandbox with the in-sandbox boost channel enabled, then has guest code
// request a boost (POST /boost on /var/run/navaris-guest.sock via curl)
// and verifies — from inside the same sandbox — that the boost actually
// took effect: MemAvailable in /proc/meminfo drops as the balloon inflates.
//
// This is the end-to-end proof that spec #3 (in-sandbox boost channel)
// composes correctly with spec #2 (live boost via balloon): the request
// goes guest → UDS → vsock → host listener → BoostHTTPHandler → BoostService
// → UpdateResources → balloon inflate, all in one round-trip, and the
// resulting resource change is observable from the same guest that
// requested it.
//
// Externally we also verify the boost shows up with source="in_sandbox",
// which is the bit consumers (UI, event subscribers) use to distinguish
// guest-initiated boosts from operator-initiated ones.
//
// Skipped on Incus for the same reason as the rest of this file: the test
// image lacks lxcfs so /proc/meminfo would report host RAM. The Incus
// boost-channel integration tests in boost_channel_test.go cover the
// channel-binding path on Incus without needing in-guest verification.
func TestBoost_E2E_FC_BoostFromInside_VisibleInGuest(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): /proc/meminfo doesn't reflect cgroup limits without lxcfs", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:          proj.ProjectID,
		Name:               "boost-e2e-fromInside",
		ImageID:            img,
		MemoryLimitMB:      &mem,
		EnableBoostChannel: ptrBoolE2E(true),
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
	t.Logf("MemAvailable before in-sandbox boost request: %d kB (~%d MiB)", memBeforeKB, memBeforeKB/1024)

	// Guest issues the boost request directly via the in-sandbox UDS — same
	// path real guest code would take.
	curl, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS -X POST http://_/boost ` +
				`-H 'Content-Type: application/json' ` +
				`-d '{"memory_limit_mb":320,"duration_seconds":30}'`},
	})
	if err != nil {
		t.Fatalf("exec curl POST /boost: %v", err)
	}
	if curl.ExitCode != 0 {
		t.Fatalf("guest curl exit %d: stderr=%s stdout=%s", curl.ExitCode, curl.Stderr, curl.Stdout)
	}
	if !strings.Contains(curl.Stdout, `"boost_id"`) {
		t.Fatalf("guest curl response missing boost_id: %s", curl.Stdout)
	}

	// Verify externally: source must be "in_sandbox" (the whole point of the
	// channel — the daemon distinguishes who asked).
	b, err := c.GetBoost(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetBoost: %v", err)
	}
	if b.Source != "in_sandbox" {
		t.Errorf("boost source = %q, want in_sandbox", b.Source)
	}

	// Balloon inflate is asynchronous.
	time.Sleep(4 * time.Second)

	memDuringKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable during in-sandbox boost: %d kB (~%d MiB)", memDuringKB, memDuringKB/1024)

	const minDropKB = 100 * 1024
	if memBeforeKB-memDuringKB < minDropKB {
		t.Errorf("expected MemAvailable to drop by at least %d kB after in-sandbox boost; before=%d kB, during=%d kB (delta=%d kB)",
			minDropKB, memBeforeKB, memDuringKB, memBeforeKB-memDuringKB)
	}

	// Cancel via the same in-sandbox channel so we exercise the DELETE path too.
	cancelCurl, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS -X DELETE -o /dev/null -w '%{http_code}' http://_/boost`},
	})
	if err != nil {
		t.Fatalf("exec curl DELETE /boost: %v", err)
	}
	if cancelCurl.ExitCode != 0 {
		t.Fatalf("guest curl DELETE exit %d: stderr=%s", cancelCurl.ExitCode, cancelCurl.Stderr)
	}
	if !strings.Contains(cancelCurl.Stdout, "204") {
		t.Errorf("expected 204 from in-sandbox cancel, got %q", cancelCurl.Stdout)
	}

	time.Sleep(4 * time.Second)

	memAfterKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable after in-sandbox cancel: %d kB (~%d MiB)", memAfterKB, memAfterKB/1024)

	const recoveryFloorKB = 50 * 1024
	if memBeforeKB-memAfterKB > recoveryFloorKB {
		t.Errorf("MemAvailable did not recover after in-sandbox cancel; before=%d kB, after=%d kB (deficit=%d kB)",
			memBeforeKB, memAfterKB, memBeforeKB-memAfterKB)
	}
}

func ptrBoolE2E(v bool) *bool { return &v }
func ptrIntE2E(v int) *int    { return &v }

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
// LOCAL-ONLY: in CI's docker-in-docker setup, Incus's `limits.cpu` integer
// (which Incus normally enforces by writing cpuset.cpus) is not effective —
// the outer Docker container's cpuset takes precedence, and `nproc` inside
// the inner container sees the full host CPU count regardless. Tests via
// /sys/devices/system/cpu/online or `nproc` therefore appear "broken" in
// CI even though the API + Incus config + `incus config set limits.cpu`
// calls are correct. On a native (non-DinD) Linux host, the cpuset IS
// enforced and this test passes. Set NAVARIS_E2E_LOCAL=1 to run it.
//
// Skipped on Firecracker because FC rejects CPU resize on running VMs
// (cpu_resize_unsupported_by_backend in internal/provider/firecracker/sandbox_resize.go).
func TestBoost_E2E_Incus_CPU_VisibleInGuest(t *testing.T) {
	requireLocalE2E(t, "Incus cpuset enforcement is unreliable inside docker-in-docker; nproc sees host CPUs even when limits.cpu is correctly applied")

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

// TestBoost_E2E_FC_CPU_AppliesToGuest creates a Firecracker sandbox with
// cpu_limit=1 and uses the boost API to grow to cpu_limit=2 within the
// boot ceiling, then verifies via in-guest workload timing that the host
// cgroup CPU bandwidth limit changed. Cancel reverts to cpu_limit=1.
//
// The test measures the wall-clock ratio of two parallel CPU-bound awk
// loops vs one serial awk in three phases:
//   - Phase A (boot, limit=1): parallel/serial >= 1.5 — throttled
//   - Phase B (boost, limit=2): parallel/serial <= 1.3 — unthrottled
//   - Phase C (cancel, limit=1): parallel/serial >= 1.5 — reverted
//
// Reading /sys/fs/cgroup/cpu.max from inside the guest does not work
// because the host cgroup is invisible to the guest kernel; workload
// timing is the only in-guest way to observe the host throttle.
//
// Requires --firecracker-vcpu-headroom-mult > 1.0 on the daemon so the
// boot ceiling is at least 2; otherwise the boost is rejected with
// exceeds_ceiling and the test skips. CI's standard FC compose uses
// headroom 2.0.
//
// Skipped on Incus.
func TestBoost_E2E_FC_CPU_AppliesToGuest(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
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

	n := calibrateAwk(t, c, sandboxID)

	measure := func(label string) float64 {
		t.Helper()
		tSerial := runAwk(t, c, sandboxID, n)
		tParallel := runAwkParallel(t, c, sandboxID, n, 2)
		ratio := float64(tParallel) / float64(tSerial)
		t.Logf("%s: serial=%s parallel=%s ratio=%.2f",
			label, time.Duration(tSerial), time.Duration(tParallel), ratio)
		return ratio
	}

	const minThrottled = 1.5
	const maxUnthrottled = 1.3

	// Phase A: boot-time enforcement (cpu_limit=1, ceiling=2 → throttled).
	if r := measure("phase A (boot, limit=1)"); r < minThrottled {
		t.Fatalf("phase A ratio = %.2f, want >= %.2f (boot-time cgroup not enforced)", r, minThrottled)
	}

	// Phase B: boost to limit=2 (== ceiling → unthrottled).
	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		CPULimit:        ptrIntE2E(2),
		DurationSeconds: 120,
	}); err != nil {
		if strings.Contains(err.Error(), "exceeds_ceiling") {
			t.Skipf("CPU boost rejected for exceeding ceiling — daemon may need --firecracker-vcpu-headroom-mult>1.0; got: %v", err)
		}
		t.Fatalf("StartBoost: %v", err)
	}

	if r := measure("phase B (boost, limit=2)"); r > maxUnthrottled {
		t.Fatalf("phase B ratio = %.2f, want <= %.2f (boost did not lift cgroup throttle)", r, maxUnthrottled)
	}

	// Phase C: cancel and verify revert.
	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}

	if r := measure("phase C (cancel, limit=1)"); r < minThrottled {
		t.Fatalf("phase C ratio = %.2f, want >= %.2f (cancel did not restore cgroup throttle)", r, minThrottled)
	}
}
