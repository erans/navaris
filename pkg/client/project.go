package client

import (
	"context"
	"fmt"
)

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (*Project, error) {
	var p Project
	if err := c.post(ctx, "/v1/projects", req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetProject retrieves a project by ID.
func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	var p Project
	if err := c.get(ctx, fmt.Sprintf("/v1/projects/%s", id), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProjects lists all projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	return getList[Project](c, ctx, "/v1/projects")
}

// UpdateProject updates a project by ID.
func (c *Client) UpdateProject(ctx context.Context, id string, req UpdateProjectRequest) (*Project, error) {
	var p Project
	if err := c.put(ctx, fmt.Sprintf("/v1/projects/%s", id), req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteProject deletes a project by ID.
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.del(ctx, fmt.Sprintf("/v1/projects/%s", id))
}
