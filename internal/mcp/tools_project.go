package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type projectListInput struct{}

type projectGetInput struct {
	ProjectID string `json:"project_id" jsonschema:"the project's ID"`
}

func registerProjectToolsImpl(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "project_list",
		Description: "List all projects in this navaris control plane.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, _ projectListInput) (*mcpsdk.CallToolResult, any, error) {
		ps, err := opts.Client.ListProjects(ctx)
		if err != nil {
			return nil, nil, err
		}
		if ps == nil {
			ps = []client.Project{}
		}
		return nil, ps, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "project_get",
		Description: "Get a single project by ID.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in projectGetInput) (*mcpsdk.CallToolResult, any, error) {
		p, err := opts.Client.GetProject(ctx, in.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		return nil, p, nil
	})
}
