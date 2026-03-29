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

	err = c.CancelOperation(ctx, op.OperationID)

	finalOp, err2 := c.WaitForOperation(ctx, op.OperationID, waitOpts())
	if err2 != nil {
		t.Fatalf("wait: %v", err2)
	}

	if finalOp.State == client.OpSucceeded && finalOp.ResourceID != "" {
		t.Cleanup(func() {
			_, _ = c.DestroySandboxAndWait(context.Background(), finalOp.ResourceID, waitOpts())
		})
	}

	t.Logf("cancel err=%v, final state=%s", err, finalOp.State)
}
