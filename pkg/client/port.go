package client

import (
	"context"
	"fmt"
)

// CreatePort publishes a port on a sandbox.
func (c *Client) CreatePort(ctx context.Context, sandboxID string, req CreatePortRequest) (*PortBinding, error) {
	var pb PortBinding
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/ports", sandboxID), req, &pb); err != nil {
		return nil, err
	}
	return &pb, nil
}

// ListPorts lists published ports for a sandbox.
func (c *Client) ListPorts(ctx context.Context, sandboxID string) ([]PortBinding, error) {
	return getList[PortBinding](c, ctx, fmt.Sprintf("/v1/sandboxes/%s/ports", sandboxID))
}

// DeletePort removes a published port from a sandbox.
func (c *Client) DeletePort(ctx context.Context, sandboxID string, targetPort int) error {
	return c.del(ctx, fmt.Sprintf("/v1/sandboxes/%s/ports/%d", sandboxID, targetPort))
}
