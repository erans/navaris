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

// runWorkload runs `head -c <bytes> /dev/zero | sha256sum > /dev/null`
// once inside the sandbox and returns elapsed wall time in nanoseconds,
// measured by the guest's own `date +%s%N` immediately before and after
// the command. Excludes the c.Exec round-trip cost.
//
// sha256sum on /dev/zero is unambiguously CPU-bound: the hash is stateful
// across the byte stream, so no interpreter or shell optimisation can skip
// the work. (An earlier awk-based implementation produced 4ns elapsed in
// CI — busybox awk evidently dead-code-eliminates BEGIN blocks with no
// observable output.)
func runWorkload(t *testing.T, c *client.Client, sandboxID string, bytes int64) int64 {
	t.Helper()
	cmd := fmt.Sprintf("T0=$(date +%%s%%N); head -c %d /dev/zero | sha256sum > /dev/null; T1=$(date +%%s%%N); echo $((T1 - T0))", bytes)
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", cmd},
	})
	if err != nil {
		t.Fatalf("runWorkload: exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("runWorkload: exit=%d stderr=%s", exec.ExitCode, exec.Stderr)
	}
	return parseElapsedNs(t, exec.Stdout)
}

// runWorkloadParallel spawns k copies of the same workload in the background
// via busybox sh, waits for all of them, and returns aggregate wall time
// in nanoseconds (the time from spawning the first to the last finishing).
func runWorkloadParallel(t *testing.T, c *client.Client, sandboxID string, bytes int64, k int) int64 {
	t.Helper()
	if k < 1 {
		t.Fatalf("runWorkloadParallel: k=%d must be >= 1", k)
	}
	var spawn strings.Builder
	for i := 0; i < k; i++ {
		spawn.WriteString(fmt.Sprintf("(head -c %d /dev/zero | sha256sum > /dev/null) & ", bytes))
	}
	cmd := fmt.Sprintf("T0=$(date +%%s%%N); %swait; T1=$(date +%%s%%N); echo $((T1 - T0))", spawn.String())
	exec, err := c.Exec(context.Background(), sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", cmd},
	})
	if err != nil {
		t.Fatalf("runWorkloadParallel: exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("runWorkloadParallel: exit=%d stderr=%s", exec.ExitCode, exec.Stderr)
	}
	return parseElapsedNs(t, exec.Stdout)
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
	calDur := time.Duration(cal)
	t.Logf("calibrate: bytes=%d took %s", calBytes, calDur)
	if calDur < 100*time.Millisecond {
		t.Skipf("calibration sample %s < 100ms; runner anomalously fast (host nearly idle), ratio signal unreliable", calDur)
	}
	if calDur > 15*time.Second {
		t.Skipf("calibration sample %s > 15s; runner anomalously slow, ratio signal unreliable", calDur)
	}
	const targetNs int64 = 3 * int64(time.Second)
	// No floor at calBytes: bytes < calBytes happens iff cal > targetNs
	// (slow runner), in which case capping bytes to calBytes would inflate
	// each measurement from the 3s target to ~cal seconds — exactly when
	// CI time budget is tight. The < 100ms skip threshold above already
	// guards against fast-runner sub-second workloads.
	bytes := int64(float64(calBytes) * float64(targetNs) / float64(cal))
	t.Logf("calibrate: chose bytes=%d for ~3s single-thread", bytes)
	return bytes
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
