package client

import (
	"context"
	"fmt"
)

// CreateSnapshot creates a snapshot of a sandbox.
// Returns the Operation tracking snapshot creation.
func (c *Client) CreateSnapshot(ctx context.Context, sandboxID string, req CreateSnapshotRequest) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/snapshots", sandboxID), req, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// GetSnapshot retrieves a snapshot by ID.
func (c *Client) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	var s Snapshot
	if err := c.get(ctx, fmt.Sprintf("/v1/snapshots/%s", id), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSnapshots lists snapshots for a sandbox.
func (c *Client) ListSnapshots(ctx context.Context, sandboxID string) ([]Snapshot, error) {
	return getList[Snapshot](c, ctx, fmt.Sprintf("/v1/sandboxes/%s/snapshots", sandboxID))
}

// RestoreSnapshot restores a sandbox to a snapshot.
// Returns the Operation tracking the restore.
func (c *Client) RestoreSnapshot(ctx context.Context, snapshotID string) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, fmt.Sprintf("/v1/snapshots/%s/restore", snapshotID), nil, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// DeleteSnapshot deletes a snapshot.
// Returns the Operation tracking the deletion.
func (c *Client) DeleteSnapshot(ctx context.Context, id string) (*Operation, error) {
	var op Operation
	if err := c.delWithResponse(ctx, fmt.Sprintf("/v1/snapshots/%s", id), &op); err != nil {
		return nil, err
	}
	return &op, nil
}
