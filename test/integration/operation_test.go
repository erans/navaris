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

	// Try to cancel. This is inherently racy — the operation may complete
	// before the cancel request arrives.
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

	// The operation must always reach a terminal state.
	if !finalOp.State.Terminal() {
		t.Fatalf("operation not terminal after cancel+wait: state=%s", finalOp.State)
	}

	switch {
	case cancelErr == nil && (finalOp.State == client.OpCancelled || finalOp.State == client.OpFailed):
		// Cancel accepted and operation was actually cancelled/failed — the ideal outcome.
		t.Logf("cancel worked: final state=%s", finalOp.State)
	case cancelErr == nil && finalOp.State == client.OpSucceeded:
		// Cancel was accepted but the operation had already finished — valid race.
		t.Log("cancel accepted but operation already succeeded (race)")
	case cancelErr != nil && finalOp.State == client.OpSucceeded:
		// Cancel rejected because operation already completed.
		t.Logf("cancel rejected (operation already complete): %v", cancelErr)
	case cancelErr != nil:
		// Cancel failed AND operation didn't succeed — unexpected.
		t.Fatalf("cancel error=%v with unexpected final state=%s", cancelErr, finalOp.State)
	}
}
