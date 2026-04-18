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

type sessionCreateInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"the sandbox to attach to"`
	Shell     string `json:"shell,omitempty" jsonschema:"shell to launch (default bash)"`
	Backing   string `json:"backing,omitempty" jsonschema:"session backing: direct or tmux (tmux survives disconnects)"`
}

type sessionDestroyInput struct {
	SessionID string `json:"session_id" jsonschema:"the session's ID"`
}

func registerSessionMutatingTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "session_create",
		Description: "Create a new interactive session attached to a running sandbox. Use backing=tmux to keep the shell alive across disconnects.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sessionCreateInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.CreateSession(ctx, in.SandboxID, client.CreateSessionRequest{
			Shell:   in.Shell,
			Backing: in.Backing,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "session_destroy",
		Description: "Destroy a session and any backing tmux state. The shell process exits.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sessionDestroyInput) (*mcpsdk.CallToolResult, any, error) {
		if err := opts.Client.DestroySession(ctx, in.SessionID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]bool{"ok": true}, nil
	})
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
