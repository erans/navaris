package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type snapshotListInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"ID of the sandbox to list snapshots for"`
}

type snapshotGetInput struct {
	SnapshotID string `json:"snapshot_id" jsonschema:"ID of the snapshot to fetch"`
}

func registerSnapshotReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_list",
		Description: "List snapshots for a sandbox.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotListInput) (*mcpsdk.CallToolResult, any, error) {
		if in.SandboxID == "" {
			return nil, nil, fmt.Errorf("sandbox_id is required")
		}
		out, err := opts.Client.ListSnapshots(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		if out == nil {
			out = []client.Snapshot{}
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_get",
		Description: "Get a single snapshot by ID.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotGetInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.GetSnapshot(ctx, in.SnapshotID)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}
