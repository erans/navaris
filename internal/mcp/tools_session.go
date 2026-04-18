package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type sessionListInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"ID of the sandbox to list sessions for"`
}

type sessionGetInput struct {
	SessionID string `json:"session_id" jsonschema:"ID of the session to fetch"`
}

func registerSessionReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "session_list",
		Description: "List interactive sessions for a sandbox.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sessionListInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.ListSessions(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		if out == nil {
			out = []client.Session{}
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "session_get",
		Description: "Get a single session by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sessionGetInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.GetSession(ctx, in.SessionID)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}
