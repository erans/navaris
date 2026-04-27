package client

import (
	"context"
	"fmt"
)

type StartBoostRequest struct {
	CPULimit        *int `json:"cpu_limit,omitempty"`
	MemoryLimitMB   *int `json:"memory_limit_mb,omitempty"`
	DurationSeconds int  `json:"duration_seconds"`
}

type Boost struct {
	BoostID               string `json:"boost_id"`
	SandboxID             string `json:"sandbox_id"`
	OriginalCPULimit      *int   `json:"original_cpu_limit"`
	OriginalMemoryLimitMB *int   `json:"original_memory_limit_mb"`
	BoostedCPULimit       *int   `json:"boosted_cpu_limit"`
	BoostedMemoryLimitMB  *int   `json:"boosted_memory_limit_mb"`
	StartedAt             string `json:"started_at"`
	ExpiresAt             string `json:"expires_at"`
	State                 string `json:"state"`
	Source                string `json:"source,omitempty"`
	RevertAttempts        int    `json:"revert_attempts,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

func (c *Client) StartBoost(ctx context.Context, sandboxID string, req StartBoostRequest) (*Boost, error) {
	var resp Boost
	if err := c.post(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetBoost(ctx context.Context, sandboxID string) (*Boost, error) {
	var b Boost
	if err := c.get(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (c *Client) CancelBoost(ctx context.Context, sandboxID string) error {
	return c.del(ctx, fmt.Sprintf("/v1/sandboxes/%s/boost", sandboxID))
}
