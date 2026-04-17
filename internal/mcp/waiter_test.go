package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

func TestWaitForOpAndFetch_ReturnsImmediatelyOnSucceeded(t *testing.T) {
	op := &client.Operation{OperationID: "op-1", State: client.OpSucceeded}
	called := 0
	res, err := waitForOpAndFetch(context.Background(), nil, op, time.Second, func() (any, error) {
		called++
		return "RESOURCE", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != "RESOURCE" {
		t.Errorf("got %v, want %q", res, "RESOURCE")
	}
	if called != 1 {
		t.Errorf("expected fetch called once, got %d", called)
	}
}

func TestWaitForOpAndFetch_FailedOpReturnsError(t *testing.T) {
	op := &client.Operation{OperationID: "op-2", State: client.OpFailed, ErrorText: "bad thing"}
	_, err := waitForOpAndFetch(context.Background(), nil, op, time.Second, func() (any, error) {
		return nil, errors.New("should not be called")
	})
	if err == nil || err.Error() == "" {
		t.Fatal("expected error from failed op")
	}
}
