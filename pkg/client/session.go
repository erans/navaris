package client

import (
	"context"
	"fmt"
)

// CreateSession creates a new session attached to a sandbox.
func (c *Client) CreateSession(ctx context.Context, sandboxID string, req CreateSessionRequest) (*Session, error) {
	var s Session
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/sessions", sandboxID), req, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSession retrieves a session by ID.
func (c *Client) GetSession(ctx context.Context, id string) (*Session, error) {
	var s Session
	if err := c.get(ctx, fmt.Sprintf("/v1/sessions/%s", id), &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSessions lists sessions for a sandbox.
func (c *Client) ListSessions(ctx context.Context, sandboxID string) ([]Session, error) {
	return getList[Session](c, ctx, fmt.Sprintf("/v1/sandboxes/%s/sessions", sandboxID))
}

// DestroySession destroys a session.
func (c *Client) DestroySession(ctx context.Context, id string) error {
	return c.del(ctx, fmt.Sprintf("/v1/sessions/%s", id))
}
