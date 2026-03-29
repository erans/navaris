//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

// TestConcurrentSandboxCreation verifies that multiple sandboxes can be
// created concurrently and all end up running. Retries on SQLITE_BUSY
// (the server's SQLite backend may contend under concurrent writes).
func TestConcurrentSandboxCreation(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	const n = 3
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		sandboxIDs []string
		errors     []error
	)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()

			// Retry on SQLITE_BUSY errors with exponential backoff.
			var lastErr error
			for attempt := 0; attempt < 5; attempt++ {
				if attempt > 0 {
					time.Sleep(time.Duration(attempt*200) * time.Millisecond)
				}
				op, err := c.CreateSandboxAndWait(ctx, client.CreateSandboxRequest{
					ProjectID: proj.ProjectID,
					Name:      fmt.Sprintf("concurrent-sbx-%d", idx),
					ImageID:   baseImage(),
				}, waitOpts())
				if err != nil {
					if isSQLiteBusy(err) {
						lastErr = err
						continue
					}
					mu.Lock()
					errors = append(errors, fmt.Errorf("sandbox %d: %w", idx, err))
					mu.Unlock()
					return
				}
				if op.State != client.OpSucceeded {
					mu.Lock()
					errors = append(errors, fmt.Errorf("sandbox %d: state=%s error=%s", idx, op.State, op.ErrorText))
					mu.Unlock()
					return
				}
				mu.Lock()
				sandboxIDs = append(sandboxIDs, op.ResourceID)
				mu.Unlock()
				return
			}
			mu.Lock()
			errors = append(errors, fmt.Errorf("sandbox %d: exhausted retries: %w", idx, lastErr))
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	t.Cleanup(func() {
		for _, id := range sandboxIDs {
			_, _ = c.DestroySandboxAndWait(context.Background(), id, waitOpts())
		}
	})

	if len(errors) > 0 {
		for _, err := range errors {
			t.Errorf("concurrent error: %v", err)
		}
		t.FailNow()
	}

	if len(sandboxIDs) != n {
		t.Fatalf("expected %d sandboxes, got %d", n, len(sandboxIDs))
	}

	for _, id := range sandboxIDs {
		sbx, err := c.GetSandbox(ctx, id)
		if err != nil {
			t.Fatalf("get sandbox %s: %v", id, err)
		}
		if sbx.State != "running" {
			t.Fatalf("sandbox %s state: %s", id, sbx.State)
		}
	}

	t.Logf("all %d sandboxes created concurrently and running", n)
}

func isSQLiteBusy(err error) bool {
	// The server returns a generic 500 for SQLITE_BUSY; the client only
	// sees "api error 500: internal server error".
	if apiErr, ok := err.(*client.APIError); ok && apiErr.StatusCode == 500 {
		return true
	}
	return strings.Contains(err.Error(), "SQLITE_BUSY") ||
		strings.Contains(err.Error(), "database is locked")
}
