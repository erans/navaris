package client

import (
	"context"
	"fmt"
	"time"
)

// GetOperation retrieves an operation by ID.
func (c *Client) GetOperation(ctx context.Context, id string) (*Operation, error) {
	var op Operation
	if err := c.get(ctx, fmt.Sprintf("/v1/operations/%s", id), &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// ListOperations lists operations with optional filters.
func (c *Client) ListOperations(ctx context.Context, sandboxID, state string) ([]Operation, error) {
	path := "/v1/operations"
	sep := "?"
	if sandboxID != "" {
		path += fmt.Sprintf("%ssandbox_id=%s", sep, sandboxID)
		sep = "&"
	}
	if state != "" {
		path += fmt.Sprintf("%sstate=%s", sep, state)
	}
	return getList[Operation](c, ctx, path)
}

// CancelOperation cancels a pending or running operation.
func (c *Client) CancelOperation(ctx context.Context, id string) error {
	_, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/operations/%s/cancel", id), nil)
	if err != nil {
		return err
	}
	return nil
}

// WaitForOperation polls an operation until it reaches a terminal state.
// Uses exponential backoff starting at 500ms, capped at 5s.
// Default timeout is 5 minutes if opts is nil or opts.Timeout is zero.
func (c *Client) WaitForOperation(ctx context.Context, id string, opts *WaitOptions) (*Operation, error) {
	timeout := DefaultWaitTimeout
	if opts != nil && opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	delay := 500 * time.Millisecond
	const maxDelay = 5 * time.Second

	for {
		op, err := c.GetOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		if op.State.Terminal() {
			return op, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for operation %s: %w", id, ctx.Err())
		case <-time.After(delay):
		}

		// Exponential backoff
		delay = delay * 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}
