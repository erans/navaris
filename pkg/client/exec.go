package client

import (
	"context"
	"fmt"
)

// Exec executes a command synchronously in a sandbox and returns the result.
func (c *Client) Exec(ctx context.Context, sandboxID string, req ExecRequest) (*ExecResponse, error) {
	var resp ExecResponse
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/exec", sandboxID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
