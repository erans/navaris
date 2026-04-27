//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// requireLocalE2E gates a test on the NAVARIS_E2E_LOCAL=1 env var. Tests that
// only work outside CI's docker-in-docker — e.g. those that need real cpuset
// enforcement, lxcfs-style cgroup reflection, or daemon-config flags that
// aren't set in the default CI compose — should call this first.
//
// reason is shown in the skip message so anyone running the suite knows why
// the test was skipped without having to read the test body.
func requireLocalE2E(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("NAVARIS_E2E_LOCAL") != "1" {
		t.Skipf("skipped: NAVARIS_E2E_LOCAL=1 not set — %s; run locally with `make e2e-local`", reason)
	}
}

// TestBoost_E2E_Local_Incus_Memory creates an Incus container with
// memory_limit_mb=512, boosts to 384 (shrink), and verifies the cgroup
// memory.max file inside the container reflects the live boost — then
// recovers after cancel.
//
// LOCAL-ONLY: setting `limits.memory` at create-time triggers
// `forkstart exit status 1` on docker-in-docker CI runners (the cgroup
// memory controller wiring fails through the nested container layer).
// On a native (non-DinD) Linux host with Incus installed and an
// incus pool/project configured, the test passes.
//
// Verification reads /sys/fs/cgroup/memory.max (cgroup v2) directly —
// this file is enforced by the kernel and visible inside the container
// even without lxcfs, unlike /proc/meminfo which would still report
// host RAM. The boost path uses the same UpdateInstance call as PATCH,
// so a passing test confirms boost-as-resize is live-applying.
//
// Skipped on Firecracker because the test image identifier is FC-style
// (no "/") — this test is only for Incus-style images.
func TestBoost_E2E_Local_Incus_Memory(t *testing.T) {
	requireLocalE2E(t, "Incus memory_limit at create-time fails forkstart in docker-in-docker; native Incus is required")

	img := baseImage()
	if !strings.Contains(img, "/") {
		t.Skipf("skipping on Firecracker (image=%s): this test is Incus-only", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "boost-e2e-local-incus-mem",
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

	beforeMiB := readCgroupMemoryMaxMiB(t, c, sandboxID)
	t.Logf("memory.max before boost: %d MiB", beforeMiB)
	if beforeMiB < 480 || beforeMiB > 540 {
		t.Fatalf("baseline memory.max = %d MiB, expected ~512 MiB", beforeMiB)
	}

	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntE2E(384),
		DurationSeconds: 30,
	}); err != nil {
		t.Fatalf("StartBoost: %v", err)
	}

	// Incus applies the cgroup write near-instantly; small delay for safety.
	time.Sleep(1 * time.Second)

	duringMiB := readCgroupMemoryMaxMiB(t, c, sandboxID)
	t.Logf("memory.max during boost: %d MiB", duringMiB)
	if duringMiB < 360 || duringMiB > 410 {
		t.Errorf("memory.max during boost = %d MiB, expected ~384 MiB", duringMiB)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}
	time.Sleep(1 * time.Second)

	afterMiB := readCgroupMemoryMaxMiB(t, c, sandboxID)
	t.Logf("memory.max after cancel: %d MiB", afterMiB)
	if afterMiB < 480 || afterMiB > 540 {
		t.Errorf("memory.max after cancel = %d MiB, expected ~512 MiB (reverted)", afterMiB)
	}
}

// TestBoost_E2E_Local_FC_Memory_Grow creates a Firecracker sandbox with
// memory_limit_mb=256 and a daemon configured with a memory headroom
// multiplier (so the VM boots at e.g. 512 MiB ceiling with the balloon
// inflated to enforce 256 MiB). It then GROWS the boost to 384 MiB and
// verifies the guest sees more available memory — i.e. the balloon
// deflated to release pages back to the guest.
//
// LOCAL-ONLY: the default CI navarisd config uses
// --firecracker-mem-headroom-mult=1.0 (no headroom), so grow boosts are
// rejected as exceeding the boot-time ceiling. To run this test you need
// a navarisd configured with --firecracker-mem-headroom-mult=2.0 or
// higher. Example local invocation:
//
//	./navarisd --firecracker-mem-headroom-mult=2.0 ...
//	NAVARIS_E2E_LOCAL=1 NAVARIS_API_URL=... go test -tags integration \
//	    ./test/integration/ -run TestBoost_E2E_Local_FC_Memory_Grow -v
//
// If the daemon doesn't have headroom configured, StartBoost returns 409
// and this test fails fast with a hint about the flag. That keeps the
// failure mode educational rather than mysterious.
//
// Skipped on Incus.
func TestBoost_E2E_Local_FC_Memory_Grow(t *testing.T) {
	requireLocalE2E(t, "FC memory grow needs --firecracker-mem-headroom-mult>1.0 on the daemon, not set in default CI")

	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): this test is Firecracker-only", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "boost-e2e-local-fc-grow",
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

	beforeKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable before grow boost: %d kB (~%d MiB)", beforeKB, beforeKB/1024)

	// Grow to 384 MiB (boost +128 over the persisted 256). Requires headroom
	// >= 1.5 on the daemon side; otherwise this returns 409.
	if _, err := c.StartBoost(ctx, sandboxID, client.StartBoostRequest{
		MemoryLimitMB:   ptrIntE2E(384),
		DurationSeconds: 30,
	}); err != nil {
		if strings.Contains(err.Error(), "exceeds_ceiling") || strings.Contains(err.Error(), "409") {
			t.Skipf("grow boost rejected: daemon likely needs --firecracker-mem-headroom-mult > 1.0; got: %v", err)
		}
		t.Fatalf("StartBoost: %v", err)
	}

	// Balloon deflate is asynchronous.
	time.Sleep(4 * time.Second)

	duringKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable during grow boost: %d kB (~%d MiB)", duringKB, duringKB/1024)

	const minGrowKB = 80 * 1024 // expect ~128 MiB; allow slack
	if duringKB-beforeKB < minGrowKB {
		t.Errorf("expected MemAvailable to grow by at least %d kB after boost; before=%d kB, during=%d kB (delta=%d kB)",
			minGrowKB, beforeKB, duringKB, duringKB-beforeKB)
	}

	if err := c.CancelBoost(ctx, sandboxID); err != nil {
		t.Fatalf("CancelBoost: %v", err)
	}
	time.Sleep(4 * time.Second)

	afterKB := readMemAvailableKB(t, c, sandboxID)
	t.Logf("MemAvailable after cancel: %d kB (~%d MiB)", afterKB, afterKB/1024)
	// After re-inflate to 256 MiB, guest sees ~beforeKB again (within slack).
	const recoveryFloorKB = 50 * 1024
	if afterKB-beforeKB > recoveryFloorKB {
		t.Errorf("MemAvailable did not contract after cancel; before=%d kB, after=%d kB (excess=%d kB)",
			beforeKB, afterKB, afterKB-beforeKB)
	}
}

// readCgroupMemoryMaxMiB reads /sys/fs/cgroup/memory.max from inside the
// sandbox and returns the limit in MiB. The file contains either "max"
// (unlimited) or a byte count. Used by Incus tests since /proc/meminfo
// inside an Incus container reports host RAM (no lxcfs in the test image).
func readCgroupMemoryMaxMiB(t *testing.T, c *client.Client, sandboxID string) int {
	t.Helper()
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "cat /sys/fs/cgroup/memory.max 2>/dev/null || cat /sys/fs/cgroup/memory/memory.limit_in_bytes"},
	})
	if err != nil {
		t.Fatalf("exec read memory.max: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("read memory.max exit %d: stderr=%s", exec.ExitCode, exec.Stderr)
	}
	raw := strings.TrimSpace(exec.Stdout)
	if raw == "max" {
		return -1
	}
	bytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("parse memory.max %q: %v", raw, err)
	}
	return int(bytes / (1024 * 1024))
}
