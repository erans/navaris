package mcp

import (
	"context"
	"errors"
	"strings"
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
	if err == nil {
		t.Fatal("expected error from failed op")
	}
	if !errors.Is(err, errOperationFailed) {
		t.Errorf("expected errors.Is(err, errOperationFailed); got %v", err)
	}
	if !strings.Contains(err.Error(), "op-2") {
		t.Errorf("expected error message to contain operation_id %q; got %q", "op-2", err.Error())
	}
}

func TestWaitForOpAndFetch_CancelledOpReturnsError(t *testing.T) {
	op := &client.Operation{OperationID: "op-3", State: client.OpCancelled}
	_, err := waitForOpAndFetch(context.Background(), nil, op, time.Second, func() (any, error) {
		return nil, errors.New("should not be called")
	})
	if err == nil {
		t.Fatal("expected error from cancelled op")
	}
	if !errors.Is(err, errOperationCancelled) {
		t.Errorf("expected errors.Is(err, errOperationCancelled); got %v", err)
	}
}

func TestResolveTimeout(t *testing.T) {
	const def = 30 * time.Second
	const max = 5 * time.Minute

	if got := resolveTimeout(0, def, max); got != def {
		t.Errorf("zero seconds: got %v, want %v", got, def)
	}
	if got := resolveTimeout(-5, def, max); got != def {
		t.Errorf("negative seconds: got %v, want %v", got, def)
	}
	if got := resolveTimeout(60, def, max); got != 60*time.Second {
		t.Errorf("in-range: got %v, want %v", got, 60*time.Second)
	}
	if got := resolveTimeout(10000, def, max); got != max {
		t.Errorf("oversized: got %v, want %v", got, max)
	}
	// default > max should be clamped to max when seconds == 0
	if got := resolveTimeout(0, 10*time.Minute, max); got != max {
		t.Errorf("default > max: got %v, want %v", got, max)
	}
}
