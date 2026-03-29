package client

import (
	"context"
	"fmt"
	"net/url"
)

// PromoteImage promotes a snapshot to a base image.
// Returns the Operation tracking the promotion.
func (c *Client) PromoteImage(ctx context.Context, req CreateImageRequest) (*Operation, error) {
	var op Operation
	if err := c.post(ctx, "/v1/images", req, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// RegisterImage registers an externally managed image.
func (c *Client) RegisterImage(ctx context.Context, req RegisterImageRequest) (*BaseImage, error) {
	var img BaseImage
	if err := c.post(ctx, "/v1/images/register", req, &img); err != nil {
		return nil, err
	}
	return &img, nil
}

// GetImage retrieves an image by ID.
func (c *Client) GetImage(ctx context.Context, id string) (*BaseImage, error) {
	var img BaseImage
	if err := c.get(ctx, fmt.Sprintf("/v1/images/%s", id), &img); err != nil {
		return nil, err
	}
	return &img, nil
}

// ListImages lists images with optional name and architecture filters.
func (c *Client) ListImages(ctx context.Context, name, architecture string) ([]BaseImage, error) {
	params := url.Values{}
	if name != "" {
		params.Set("name", name)
	}
	if architecture != "" {
		params.Set("architecture", architecture)
	}
	path := "/v1/images"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return getList[BaseImage](c, ctx, path)
}

// DeleteImage deletes an image.
// Returns the Operation tracking the deletion.
func (c *Client) DeleteImage(ctx context.Context, id string) (*Operation, error) {
	var op Operation
	if err := c.delWithResponse(ctx, fmt.Sprintf("/v1/images/%s", id), &op); err != nil {
		return nil, err
	}
	return &op, nil
}
