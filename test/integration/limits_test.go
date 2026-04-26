//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestSandbox_HonorsRequestedMemoryLimit creates a sandbox with
// memory_limit_mb=512 and verifies that the guest's MemTotal in
// /proc/meminfo lands in a sensible band around 512 MiB. The band
// accounts for kernel + initramfs reserved memory; we assert
// "approximately 512 MiB", not equality.
//
// Runs against both Incus and Firecracker via the matrix — both
// backends now honor the requested limit (Incus always has;
// Firecracker as of #PR <this one>).
func TestSandbox_HonorsRequestedMemoryLimit(t *testing.T) {
	if img := os.Getenv("NAVARIS_BASE_IMAGE"); strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s): the test image lacks lxcfs, so /proc/meminfo reports host RAM not container limit; Firecracker still exercises this path", img)
	}
	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 512
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:     proj.ProjectID,
		Name:          "limits-mem-512",
		ImageID:       baseImage(),
		MemoryLimitMB: &mem,
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})

	// Read MemTotal (kB) from /proc/meminfo.
	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c", "awk '/^MemTotal:/ {print $2}' /proc/meminfo"},
	})
	if err != nil {
		t.Fatalf("exec MemTotal: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec MemTotal exit %d: %s", exec.ExitCode, exec.Stderr)
	}
	memKB, err := strconv.Atoi(strings.TrimSpace(exec.Stdout))
	if err != nil {
		t.Fatalf("parse MemTotal %q: %v", exec.Stdout, err)
	}
	memMiB := memKB / 1024

	// Band: 460..520 MiB. Lower end accounts for kernel + initramfs
	// reservations (typically ~30-50 MiB on a minimal Alpine guest);
	// upper end leaves a small allowance for measurement variance.
	const lo, hi = 460, 520
	if memMiB < lo || memMiB > hi {
		t.Errorf("guest MemTotal = %d MiB, expected %d..%d MiB (requested 512 MB)", memMiB, lo, hi)
	}
}
