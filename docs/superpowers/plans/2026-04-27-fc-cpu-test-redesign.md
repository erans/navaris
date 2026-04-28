# FC CPU Integration Test Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two `t.Skipf`-ing FC CPU integration tests with workload-based throttle detection that compares wall-clock time of two parallel CPU-bound jobs against one serial job inside the guest.

**Architecture:** Three private helpers in a new `cpu_workload_helpers_test.go` file, consumed by `TestSandbox_HonorsRequestedCPULimit` (rewritten body) and `TestBoost_E2E_FC_CPU_AppliesToGuest` (replaced with three-phase ratio measurement). The tests assert `parallel/serial ≥ 1.5` when throttled, `≤ 1.3` when not. Calibration tunes a per-runner iteration count so single-thread time targets ~3s.

**Tech Stack:** Go (`testing` package), `pkg/client.Client.Exec`, busybox awk inside the alpine FC guest.

**Spec:** [docs/superpowers/specs/2026-04-27-fc-cpu-test-redesign-design.md](../specs/2026-04-27-fc-cpu-test-redesign-design.md)

---

## File Plan

### Created
- `test/integration/cpu_workload_helpers_test.go` — three helpers: `runAwk`, `runAwkParallel`, `calibrateAwk`. Build-tagged `integration`.

### Modified
- `test/integration/limits_cpu_test.go` — full rewrite of `TestSandbox_HonorsRequestedCPULimit` body. Old guest-cgroupfs read deleted.
- `test/integration/boost_e2e_test.go` — replace `TestBoost_E2E_FC_CPU_AppliesToGuest` body with three-phase ratio test. Delete `readGuestCPUQuota` helper (used only by this test).

No production code changes. No CI compose changes. Build tags untouched.

---

## Conventions

- Each task ends with a `gofmt -l <files>` check, a compile-only check (`go test -c -tags integration -o /dev/null ./test/integration/`), `go vet -tags integration ./test/integration/`, and a commit.
- Match existing commit prefixes: `test(integration):`, `refactor(test):`.
- Tests cannot be run locally without a working FC daemon environment; verify via compile+vet+CI run on a PR.

---

## Task 1: Add the workload helpers file

**Files:**
- Create: `test/integration/cpu_workload_helpers_test.go`

The helpers run awk inside the guest via `c.Exec`, measuring wall time *inside the guest* with `date +%s%N` so the cost of the Exec round-trip is excluded.

- [ ] **Step 1: Create the file**

Create `test/integration/cpu_workload_helpers_test.go` with the following content:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// runAwk runs `awk 'BEGIN{s=0;for(i=0;i<n;i++)s+=i}'` once inside the
// sandbox and returns elapsed wall time in nanoseconds, measured by the
// guest's own `date +%s%N` immediately before and after the awk command.
// Excludes the c.Exec round-trip cost.
func runAwk(t *testing.T, c *client.Client, sandboxID string, n int64) int64 {
	t.Helper()
	cmd := fmt.Sprintf("T0=$(date +%%s%%N); awk -v n=%d 'BEGIN{s=0;for(i=0;i<n;i++)s+=i}'; T1=$(date +%%s%%N); echo $((T1 - T0))", n)
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", cmd},
	})
	if err != nil {
		t.Fatalf("runAwk: exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("runAwk: exit=%d stderr=%s", exec.ExitCode, exec.Stderr)
	}
	return parseElapsedNs(t, exec.Stdout)
}

// runAwkParallel spawns k copies of the same awk in the background via
// busybox sh, waits for all of them, and returns aggregate wall time
// in nanoseconds (the time from spawning the first to the last finishing).
func runAwkParallel(t *testing.T, c *client.Client, sandboxID string, n int64, k int) int64 {
	t.Helper()
	if k < 1 {
		t.Fatalf("runAwkParallel: k=%d must be >= 1", k)
	}
	var spawn strings.Builder
	for i := 0; i < k; i++ {
		spawn.WriteString(fmt.Sprintf("awk -v n=%d 'BEGIN{s=0;for(i=0;i<n;i++)s+=i}' & ", n))
	}
	cmd := fmt.Sprintf("T0=$(date +%%s%%N); %swait; T1=$(date +%%s%%N); echo $((T1 - T0))", spawn.String())
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", cmd},
	})
	if err != nil {
		t.Fatalf("runAwkParallel: exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("runAwkParallel: exit=%d stderr=%s", exec.ExitCode, exec.Stderr)
	}
	return parseElapsedNs(t, exec.Stdout)
}

// calibrateAwk runs one short awk inside the guest and computes an n
// that targets ~3s of single-threaded wall time. Skips the test (via
// t.Skipf) when the calibration sample is < 0.5s or > 15s — these
// bracket "runner so fast/slow that the differential signal isn't
// reliable". Returns the calibrated iteration count.
func calibrateAwk(t *testing.T, c *client.Client, sandboxID string) int64 {
	t.Helper()
	const calN = 10_000_000
	cal := runAwk(t, c, sandboxID, calN)
	calDur := time.Duration(cal)
	t.Logf("calibrate: n=%d took %s", calN, calDur)
	if calDur < 500*time.Millisecond {
		t.Skipf("calibration sample %s < 500ms; runner anomalously fast or under-load, ratio signal unreliable", calDur)
	}
	if calDur > 15*time.Second {
		t.Skipf("calibration sample %s > 15s; runner anomalously slow, ratio signal unreliable", calDur)
	}
	const targetNs int64 = 3 * int64(time.Second)
	n := int64(float64(calN) * float64(targetNs) / float64(cal))
	if n < calN {
		n = calN // floor at calibration N so we never measure on a sub-second workload
	}
	t.Logf("calibrate: chose n=%d for ~3s single-thread", n)
	return n
}

// parseElapsedNs parses the stdout of a wall-time-printing shell command
// (one line, integer nanoseconds).
func parseElapsedNs(t *testing.T, stdout string) int64 {
	t.Helper()
	s := strings.TrimSpace(stdout)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("parseElapsedNs: bad output %q: %v", s, err)
	}
	if v <= 0 {
		t.Fatalf("parseElapsedNs: non-positive elapsed %d", v)
	}
	return v
}
```

- [ ] **Step 2: gofmt**

```bash
gofmt -l test/integration/cpu_workload_helpers_test.go
```

Expected: no output (file is gofmt-clean).

- [ ] **Step 3: Compile + vet**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 4: Commit**

```bash
git add test/integration/cpu_workload_helpers_test.go
git commit -m "test(integration): add awk workload helpers for FC CPU throttle tests"
```

---

## Task 2: Rewrite `TestSandbox_HonorsRequestedCPULimit`

**Files:**
- Modify: `test/integration/limits_cpu_test.go`

Replace the test body (and trim imports) so the test creates a sandbox with `cpu_limit=1`, calibrates, measures the parallel/serial ratio, and asserts `≥ 1.5`.

- [ ] **Step 1: Replace the file content**

Overwrite `test/integration/limits_cpu_test.go` with:

```go
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
```

- [ ] **Step 2: gofmt**

```bash
gofmt -l test/integration/limits_cpu_test.go
```

Expected: no output.

- [ ] **Step 3: Compile + vet**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 4: Commit**

```bash
git add test/integration/limits_cpu_test.go
git commit -m "test(integration): rewrite TestSandbox_HonorsRequestedCPULimit using workload ratio"
```

---

## Task 3: Replace `TestBoost_E2E_FC_CPU_AppliesToGuest` and delete `readGuestCPUQuota`

**Files:**
- Modify: `test/integration/boost_e2e_test.go`

Replace the existing test body (lines 343–414) with the three-phase ratio test. Delete the now-unused `readGuestCPUQuota` helper (lines 416–439).

- [ ] **Step 1: Read the surrounding context**

```bash
sed -n '330,440p' test/integration/boost_e2e_test.go
```

Confirm the structure: the test starts at `func TestBoost_E2E_FC_CPU_AppliesToGuest(t *testing.T) {` and the helper begins at `func readGuestCPUQuota(`.

- [ ] **Step 2: Replace the test + delete the helper**

Use Edit with `old_string` covering the existing test body AND the helper function (one block from `// TestBoost_E2E_FC_CPU_AppliesToGuest creates a Firecracker sandbox with` through the closing `}` of `readGuestCPUQuota`), and `new_string` containing only the rewritten test:

```go
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
```

The Edit replaces both the old test body and the entire `readGuestCPUQuota` function with the new test. After the edit, `readGuestCPUQuota` is gone.

- [ ] **Step 3: Verify `readGuestCPUQuota` is gone**

```bash
grep -n "readGuestCPUQuota" test/integration/
```

Expected: no output (function deleted, no remaining callers).

- [ ] **Step 4: Verify `strconv` is no longer used in this file** (it was imported only for `readGuestCPUQuota`)

```bash
grep -n '"strconv"' test/integration/boost_e2e_test.go
grep -n "strconv\." test/integration/boost_e2e_test.go
```

If the import remains but no usages exist, remove the import. The new test does not use `strconv`. (If `strconv` is used elsewhere in the file by another existing test, leave the import alone — verify by counting usages in the second grep.)

- [ ] **Step 5: gofmt**

```bash
gofmt -l test/integration/boost_e2e_test.go
```

Expected: no output. If gofmt reports the file, run `gofmt -w test/integration/boost_e2e_test.go` and inspect.

- [ ] **Step 6: Compile + vet**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 7: Commit**

```bash
git add test/integration/boost_e2e_test.go
git commit -m "test(integration): rewrite TestBoost_E2E_FC_CPU_AppliesToGuest using workload ratio"
```

---

## Task 4: Final verification + push

**Files:** none (verification only).

- [ ] **Step 1: Full unit test matrix** (sanity — should be unaffected)

```bash
go test ./...
go test -tags incus ./...
go test -tags firecracker ./...
go test -tags 'incus firecracker' ./...
```

All four green.

- [ ] **Step 2: Integration compile + vet**

```bash
go test -c -tags integration -o /dev/null ./test/integration/
go vet -tags integration ./test/integration/
```

Both clean.

- [ ] **Step 3: Confirm three commits on the branch**

```bash
git log --oneline main..HEAD
```

Expected output (3 commits, in order):
```
<sha> test(integration): rewrite TestBoost_E2E_FC_CPU_AppliesToGuest using workload ratio
<sha> test(integration): rewrite TestSandbox_HonorsRequestedCPULimit using workload ratio
<sha> test(integration): add awk workload helpers for FC CPU throttle tests
```

- [ ] **Step 4: Push and open PR**

```bash
git push -u origin <current-branch-name>
gh pr create --title "test(integration): replace FC CPU tests with workload-based throttle detection" \
  --body "$(cat <<'EOF'
## Summary

- Replace `TestSandbox_HonorsRequestedCPULimit` and `TestBoost_E2E_FC_CPU_AppliesToGuest` with workload-based throttle detection that measures the wall-clock ratio of two parallel CPU-bound awk loops vs one serial awk inside the guest.
- The previous tests read `/sys/fs/cgroup/cpu.max` from inside the guest, which cannot observe the host cgroup limit (the guest kernel has its own independent cgroup hierarchy under KVM). Both tests `t.Skipf`-ed in CI; this PR makes them actually exercise the throttle.

## Spec

[docs/superpowers/specs/2026-04-27-fc-cpu-test-redesign-design.md](docs/superpowers/specs/2026-04-27-fc-cpu-test-redesign-design.md)

## Test plan

- [x] `go test ./...` (and the three tag variants)
- [x] `go test -c -tags integration` + `go vet -tags integration ./test/integration/`
- [ ] CI exercises the new tests (no longer skipped on FC); they should `PASS` rather than `SKIP`.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Monitor CI**

After PR creation, watch the integration jobs. Both `integration (alpine-3.21)` / `integration (debian-12)` and `integration-firecracker-cow (*)` should now PASS the two CPU tests rather than SKIP. The full FC suite should still fit under the 15m test timeout.

---

## Notes for the executing agent

- These are **integration tests** — they cannot be run locally without a fully-staged FC environment (kernel + rootfs + privileged docker). Don't attempt to run them via `go test`. Compile-only + vet is the local verification.
- The `parallel/serial` ratio is a behavioural assertion — if CI flakes, the right next step is to add best-of-3 retry per measurement (mentioned in the spec as a follow-up), NOT to widen the bands. Borderline values should be investigated.
- The calibration N is computed in-test; do not hard-code it.
- If `TestBoost_E2E_FC_CPU_AppliesToGuest` skips with `exceeds_ceiling`, the daemon was started without `--firecracker-vcpu-headroom-mult > 1.0`. The CI compose has it; local runs may not.
