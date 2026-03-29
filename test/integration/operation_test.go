//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestOperationListAndGet(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "op-test-sbx",
		ImageID:   baseImage(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	finalOp, err := c.WaitForOperation(ctx, op.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	sandboxID := finalOp.ResourceID
	t.Cleanup(func() {
		_, _ = c.DestroySandboxAndWait(context.Background(), sandboxID, waitOpts())
	})

	got, err := c.GetOperation(ctx, op.OperationID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if got.OperationID != op.OperationID {
		t.Fatalf("operation ID mismatch: %s vs %s", got.OperationID, op.OperationID)
	}
	if got.State != client.OpSucceeded {
		t.Fatalf("expected succeeded, got %s", got.State)
	}

	ops, err := c.ListOperations(ctx, sandboxID, "")
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("expected at least one operation")
	}
	found := false
	for _, o := range ops {
		if o.OperationID == op.OperationID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("operation not found in list")
	}
}

func TestOperationCancel(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)

	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "cancel-test-sbx",
		ImageID:   baseImage(),
	})
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Try to cancel. This may or may not succeed depending on timing.
	cancelErr := c.CancelOperation(ctx, op.OperationID)

	finalOp, err := c.WaitForOperation(ctx, op.OperationID, waitOpts())
	if err != nil {
		t.Fatalf("wait: %v", err)
	}

	if finalOp.State == client.OpSucceeded && finalOp.ResourceID != "" {
		t.Cleanup(func() {
			_, _ = c.DestroySandboxAndWait(context.Background(), finalOp.ResourceID, waitOpts())
		})
	}

	// If cancel succeeded (no error), the operation must be in a terminal state
	// that isn't "succeeded" — it should be cancelled or failed.
	// If the operation completed before cancel, cancel may return an error
	// (already completed) or the state is succeeded — both are acceptable races.
	if cancelErr == nil && finalOp.State == client.OpSucceeded {
		t.Log("cancel returned nil but operation succeeded — race between cancel and completion (acceptable)")
	} else if cancelErr == nil {
		if !finalOp.State.Terminal() {
			t.Fatalf("cancel succeeded but operation is not terminal: state=%s", finalOp.State)
		}
		t.Logf("cancel succeeded, final state=%s", finalOp.State)
	} else {
		t.Logf("cancel returned error (likely already completed): %v, final state=%s", cancelErr, finalOp.State)
	}
}
