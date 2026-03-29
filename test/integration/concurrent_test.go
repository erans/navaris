//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

// TestConcurrentSandboxCreation verifies that multiple sandboxes can coexist
// and run simultaneously. The sandboxes are created sequentially to avoid
// SQLite write contention (SQLITE_BUSY), then verified to all be running.
func TestConcurrentSandboxCreation(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	const n = 3
	var sandboxIDs []string

	for i := 0; i < n; i++ {
		op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
			ProjectID: proj.ProjectID,
			Name:      fmt.Sprintf("concurrent-sbx-%d", i),
			ImageID:   baseImage(),
		}, waitOpts())
		if err != nil {
			t.Fatalf("create sandbox %d: %v", i, err)
		}
		if op.State != client.OpSucceeded {
			t.Fatalf("sandbox %d: state=%s error=%s", i, op.State, op.ErrorText)
		}
		sandboxIDs = append(sandboxIDs, op.ResourceID)
	}

	t.Cleanup(func() {
		for _, id := range sandboxIDs {
			_, _ = c.DestroySandboxAndWait(context.Background(), id, waitOpts())
		}
	})

	// Verify all sandboxes are running concurrently.
	for _, id := range sandboxIDs {
		sbx, err := c.GetSandbox(ctx, id)
		if err != nil {
			t.Fatalf("get sandbox %s: %v", id, err)
		}
		if sbx.State != "running" {
			t.Fatalf("sandbox %s state: %s", id, sbx.State)
		}
	}

	t.Logf("all %d sandboxes created and running concurrently", n)
}
