//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

func ptrBoolChan(v bool) *bool { return &v }

// TestBoostChannel_FC_StartFromInside creates an FC sandbox with the boost
// channel enabled, exec's curl from inside to POST /boost on the local UDS,
// and verifies the boost shows up via the external GET /v1/sandboxes/{id}/boost.
//
// Skipped on Incus per spec #2's pattern: limits.memory on Incus create
// breaks forkstart in this CI environment.
func TestBoostChannel_FC_StartFromInside(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s); see spec #2 task 13 note", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:          proj.ProjectID,
		Name:               "boostchan-fc",
		ImageID:            img,
		MemoryLimitMB:      &mem,
		EnableBoostChannel: ptrBoolChan(true),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s error=%s", op.State, op.ErrorText)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS -X POST http://_/boost ` +
				`-H 'Content-Type: application/json' ` +
				`-d '{"memory_limit_mb":192,"duration_seconds":3}'`},
	})
	if err != nil {
		t.Fatalf("exec curl: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec exit %d: stderr=%s stdout=%s", exec.ExitCode, exec.Stderr, exec.Stdout)
	}
	if !strings.Contains(exec.Stdout, `"boost_id"`) {
		t.Fatalf("response missing boost_id: %s", exec.Stdout)
	}

	b, err := c.GetBoost(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetBoost: %v", err)
	}
	if b.State != "active" {
		t.Errorf("state = %s", b.State)
	}

	time.Sleep(5 * time.Second)
	if _, err := c.GetBoost(ctx, sandboxID); err == nil {
		t.Fatal("expected 404 after expiry")
	}
}

func TestBoostChannel_FC_OptOut(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	mem := 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:          proj.ProjectID,
		Name:               "boostchan-optout",
		ImageID:            img,
		MemoryLimitMB:      &mem,
		EnableBoostChannel: ptrBoolChan(false),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s", op.State)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"test", "-e", "/var/run/navaris-guest.sock"},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exec.ExitCode == 0 {
		t.Fatal("/var/run/navaris-guest.sock should not exist when opt-out")
	}
}

func TestBoostChannel_FC_GetSandbox(t *testing.T) {
	img := baseImage()
	if strings.Contains(img, "/") {
		t.Skipf("skipping on Incus (image=%s)", img)
	}

	c := newClient()
	ctx := context.Background()
	proj := createTestProject(t, c)

	cpu, mem := 1, 256
	op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
		ProjectID:          proj.ProjectID,
		Name:               "boostchan-info",
		ImageID:            img,
		CPULimit:           &cpu,
		MemoryLimitMB:      &mem,
		EnableBoostChannel: ptrBoolChan(true),
	}, waitOpts())
	if err != nil {
		t.Fatalf("CreateSandboxAndWait: %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("create op state=%s", op.State)
	}
	sandboxID := op.ResourceID
	t.Cleanup(func() { _, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts()) })

	exec, err := c.Exec(ctx, sandboxID, client.ExecRequest{
		Command: []string{"sh", "-c",
			`curl --unix-socket /var/run/navaris-guest.sock -sS http://_/sandbox`},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if exec.ExitCode != 0 {
		t.Fatalf("exec exit %d: %s", exec.ExitCode, exec.Stderr)
	}
	if !strings.Contains(exec.Stdout, sandboxID) {
		t.Errorf("response missing sandbox id: %s", exec.Stdout)
	}
}
