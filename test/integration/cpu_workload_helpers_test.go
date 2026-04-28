//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// runWorkload runs `head -c <bytes> /dev/zero | sha256sum > /dev/null`
// once inside the sandbox and returns elapsed wall time.
//
// Wall time is measured on the test (Go) side, not via `date` inside the
// guest: busybox `date` in the alpine rootfs ignores the `%N` (nanosecond)
// format specifier and returns only seconds, which is far too coarse for a
// 3s workload. The Exec round-trip on the local Docker network adds <50ms
// and is identical across calls, so it cancels out of the cross-phase
// comparison used by the throttle assertion.
//
// sha256sum on /dev/zero is unambiguously CPU-bound: the hash is stateful
// across the byte stream, so no interpreter or shell optimisation can skip
// the work.
func runWorkload(t *testing.T, c *client.Client, sandboxID string, bytes int64) time.Duration {
	t.Helper()
	cmd := fmt.Sprintf("head -c %d /dev/zero | sha256sum > /dev/null", bytes)
	start := time.Now()
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", cmd},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runWorkload: exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("runWorkload: exit=%d stderr=%s", exec.ExitCode, exec.Stderr)
	}
	return elapsed
}

// calibrateWorkload runs one short workload inside the guest and computes
// a byte count that targets ~3s of single-threaded wall time. Skips the
// test (via t.Skipf) when the calibration sample is < 100ms or > 15s —
// these bracket "runner so fast/slow that the differential signal isn't
// reliable". Returns the calibrated byte count.
func calibrateWorkload(t *testing.T, c *client.Client, sandboxID string) int64 {
	t.Helper()
	const calBytes int64 = 64 * 1024 * 1024 // 64 MiB
	cal := runWorkload(t, c, sandboxID, calBytes)
	t.Logf("calibrate: bytes=%d took %s", calBytes, cal)
	if cal < 100*time.Millisecond {
		t.Skipf("calibration sample %s < 100ms; runner anomalously fast (host nearly idle), ratio signal unreliable", cal)
	}
	if cal > 15*time.Second {
		t.Skipf("calibration sample %s > 15s; runner anomalously slow, ratio signal unreliable", cal)
	}
	const target = 3 * time.Second
	// No floor at calBytes: bytes < calBytes happens iff cal > target
	// (slow runner), in which case capping bytes to calBytes would inflate
	// each measurement from the 3s target to ~cal — exactly when CI time
	// budget is tight. The < 100ms skip threshold above already guards
	// against fast-runner sub-second workloads.
	bytes := int64(float64(calBytes) * float64(target) / float64(cal))
	t.Logf("calibrate: chose bytes=%d for ~3s single-thread", bytes)
	return bytes
}
