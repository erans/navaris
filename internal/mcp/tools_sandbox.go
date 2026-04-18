package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type sandboxListInput struct {
	ProjectID string `json:"project_id" jsonschema:"ID of the project to list sandboxes from"`
	State     string `json:"state,omitempty" jsonschema:"optional state filter (running, stopped, ...)"`
}

type sandboxGetInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"ID of the sandbox to fetch"`
}

func registerSandboxReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_list",
		Description: "List sandboxes in a project. Optionally filter by state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxListInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.ListSandboxes(ctx, in.ProjectID, in.State)
		if err != nil {
			return nil, nil, err
		}
		if out == nil {
			out = []client.Sandbox{}
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_get",
		Description: "Get a single sandbox by ID, including its current state and backend.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxGetInput) (*mcpsdk.CallToolResult, any, error) {
		sbx, err := opts.Client.GetSandbox(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		return nil, sbx, nil
	})
}
