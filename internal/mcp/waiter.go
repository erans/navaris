package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/navaris/navaris/pkg/client"
)

var (
	errOperationFailed    = errors.New("operation failed")
	errOperationCancelled = errors.New("operation cancelled")
)

// progressResponse is the shape returned when a per-tool timeout elapses but
// the underlying operation is still running. Returned as a non-error result so
// the agent can decide whether to keep polling via operation_get.
type progressResponse struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	Note        string `json:"note"`
}

// waitForOpAndFetch waits for op to reach a terminal state (or until timeout
// elapses), then calls fetch to return the up-to-date resource.
//
//   - If op is already succeeded, fetch is called immediately.
//   - If op fails, returns an error including the operation_id and error_text.
//   - If op is cancelled, returns an error.
//   - If timeout elapses while op is still running, returns a non-error
//     progress payload so the agent can poll operation_get.
//
// Callers should return the (any, error) pair directly to the MCP SDK — a
// returned progressResponse value is the intended response when the timeout
// elapses and should not be special-cased.
func waitForOpAndFetch(
	ctx context.Context,
	c *client.Client,
	op *client.Operation,
	timeout time.Duration,
	fetch func() (any, error),
) (any, error) {
	if op == nil {
		return nil, errors.New("waitForOpAndFetch: nil operation")
	}
	if op.State == client.OpSucceeded {
		return fetch()
	}
	if op.State == client.OpFailed {
		return nil, fmt.Errorf("%w: %s: %s", errOperationFailed, op.OperationID, op.ErrorText)
	}
	if op.State == client.OpCancelled {
		return nil, fmt.Errorf("%w: %s", errOperationCancelled, op.OperationID)
	}

	final, err := c.WaitForOperation(ctx, op.OperationID, &client.WaitOptions{Timeout: timeout})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return progressResponse{
				OperationID: op.OperationID,
				Status:      "running",
				Note:        "still in progress, poll operation_get",
			}, nil
		}
		return nil, fmt.Errorf("wait for operation %s: %w", op.OperationID, err)
	}
	if final == nil {
		return nil, fmt.Errorf("wait for operation %s: client returned nil operation", op.OperationID)
	}
	if final.State == client.OpFailed {
		return nil, fmt.Errorf("%w: %s: %s", errOperationFailed, final.OperationID, final.ErrorText)
	}
	if final.State == client.OpCancelled {
		return nil, fmt.Errorf("%w: %s", errOperationCancelled, final.OperationID)
	}
	return fetch()
}

// resolveTimeout converts an agent-supplied timeout_seconds (0 = use default)
// into a time.Duration, capped at maxTimeout.
func resolveTimeout(seconds int, defaultDur, maxDur time.Duration) time.Duration {
	if defaultDur > maxDur {
		defaultDur = maxDur
	}
	if seconds <= 0 {
		return defaultDur
	}
	d := time.Duration(seconds) * time.Second
	if d <= 0 || d > maxDur {
		return maxDur
	}
	return d
}
