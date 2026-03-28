package client

import (
	"context"
	"fmt"
)

// CreateSandbox creates a new sandbox from an image.
// Returns the Operation tracking sandbox creation.
func (c *Client) CreateSandbox(ctx context.Context, req CreateSandboxRequest) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, "/v1/sandboxes", req, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// CreateSandboxFromSnapshot creates a new sandbox from a snapshot.
// Returns the Operation tracking sandbox creation.
func (c *Client) CreateSandboxFromSnapshot(ctx context.Context, req CreateSandboxFromSnapshotRequest) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, "/v1/sandboxes/from-snapshot", req, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// GetSandbox retrieves a sandbox by ID.
func (c *Client) GetSandbox(ctx context.Context, id string) (*Sandbox, error) {
	var s Sandbox
	if err := c.get(ctx, fmt.Sprintf("/v1/sandboxes/%s", id), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSandboxes lists sandboxes for a project, optionally filtering by state.
func (c *Client) ListSandboxes(ctx context.Context, projectID string, state string) ([]Sandbox, error) {
	path := fmt.Sprintf("/v1/sandboxes?project_id=%s", projectID)
	if state != "" {
		path += fmt.Sprintf("&state=%s", state)
	}
	return getList[Sandbox](c, ctx, path)
}

// StartSandbox starts a stopped sandbox.
// Returns the Operation tracking the start.
func (c *Client) StartSandbox(ctx context.Context, id string) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/start", id), nil, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// StopSandbox stops a running sandbox.
// Returns the Operation tracking the stop.
func (c *Client) StopSandbox(ctx context.Context, id string, req StopSandboxRequest) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/stop", id), req, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// DestroySandbox destroys a sandbox.
// Returns the Operation tracking the destruction.
func (c *Client) DestroySandbox(ctx context.Context, id string) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/destroy", id), nil, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// CreateSandboxAndWait creates a sandbox and waits for the operation to complete.
func (c *Client) CreateSandboxAndWait(ctx context.Context, req CreateSandboxRequest, opts *WaitOptions) (*Operation, error) {
	op, err := c.CreateSandbox(ctx, req)
	if err != nil {
		return nil, err
	}
	return c.WaitForOperation(ctx, op.OperationID, opts)
}

// StartSandboxAndWait starts a sandbox and waits for the operation to complete.
func (c *Client) StartSandboxAndWait(ctx context.Context, id string, opts *WaitOptions) (*Operation, error) {
	op, err := c.StartSandbox(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.WaitForOperation(ctx, op.OperationID, opts)
}

// StopSandboxAndWait stops a sandbox and waits for the operation to complete.
func (c *Client) StopSandboxAndWait(ctx context.Context, id string, req StopSandboxRequest, opts *WaitOptions) (*Operation, error) {
	op, err := c.StopSandbox(ctx, id, req)
	if err != nil {
		return nil, err
	}
	return c.WaitForOperation(ctx, op.OperationID, opts)
}

// DestroySandboxAndWait destroys a sandbox and waits for the operation to complete.
func (c *Client) DestroySandboxAndWait(ctx context.Context, id string, opts *WaitOptions) (*Operation, error) {
	op, err := c.DestroySandbox(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.WaitForOperation(ctx, op.OperationID, opts)
}
