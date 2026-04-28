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
		t.Skipf("calibration sample %s < 500ms; runner anomalously fast (host nearly idle), ratio signal unreliable", calDur)
	}
	if calDur > 15*time.Second {
		t.Skipf("calibration sample %s > 15s; runner anomalously slow, ratio signal unreliable", calDur)
	}
	const targetNs int64 = 3 * int64(time.Second)
	// No floor at calN: n < calN happens iff cal > targetNs (slow runner),
	// in which case capping n to calN would inflate each measurement from
	// the 3s target to ~cal seconds — exactly when CI time budget is tight.
	// The < 500ms skip threshold above already guards against fast-runner
	// sub-second workloads (where n > calN by construction).
	n := int64(float64(calN) * float64(targetNs) / float64(cal))
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
