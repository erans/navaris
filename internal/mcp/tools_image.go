package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type imageListInput struct {
	Name         string `json:"name,omitempty" jsonschema:"optional name filter"`
	Architecture string `json:"architecture,omitempty" jsonschema:"optional architecture filter (e.g. amd64, arm64)"`
}

type imageGetInput struct {
	ImageID string `json:"image_id" jsonschema:"ID of the image to fetch"`
}

func registerImageTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "image_list",
		Description: "List base images. Optionally filter by name and/or architecture.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in imageListInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.ListImages(ctx, in.Name, in.Architecture)
		if err != nil {
			return nil, nil, err
		}
		if out == nil {
			out = []client.BaseImage{}
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "image_get",
		Description: "Get a single base image by ID, including its name, version, backend, architecture, and state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in imageGetInput) (*mcpsdk.CallToolResult, any, error) {
		img, err := opts.Client.GetImage(ctx, in.ImageID)
		if err != nil {
			return nil, nil, err
		}
		return nil, img, nil
	})
}
