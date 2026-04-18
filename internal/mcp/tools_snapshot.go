package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

const (
	// snapshotCreateDefaultTimeout bounds how long snapshot_create waits for
	// the snapshot to reach the ready state. Two minutes covers worst-case
	// filesystem quiescing; backends that complete faster return immediately.
	snapshotCreateDefaultTimeout = 2 * time.Minute
	// snapshotRestoreDefaultTimeout bounds how long snapshot_restore waits for
	// the rollback to complete. Five minutes covers full-disk restores on slow
	// storage backends where the sandbox must also be restarted post-rollback.
	snapshotRestoreDefaultTimeout = 5 * time.Minute
	// snapshotDeleteDefaultTimeout bounds how long snapshot_delete waits for
	// the deletion to complete. One minute is generous for an async delete.
	snapshotDeleteDefaultTimeout = time.Minute
)

type snapshotListInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"ID of the sandbox to list snapshots for"`
}

type snapshotGetInput struct {
	SnapshotID string `json:"snapshot_id" jsonschema:"ID of the snapshot to fetch"`
}

type snapshotCreateInput struct {
	SandboxID       string `json:"sandbox_id" jsonschema:"the sandbox to snapshot"`
	Label           string `json:"label,omitempty" jsonschema:"optional human-readable label"`
	ConsistencyMode string `json:"consistency_mode,omitempty" jsonschema:"optional consistency mode"`
	Wait            *bool  `json:"wait,omitempty" jsonschema:"wait for the snapshot to be ready (default true)"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
}

type snapshotRestoreInput struct {
	SnapshotID     string `json:"snapshot_id" jsonschema:"the snapshot to restore"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait for the operation to reach terminal state (default true)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
}

type snapshotDeleteInput struct {
	SnapshotID     string `json:"snapshot_id" jsonschema:"the snapshot to delete"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait for the operation to reach terminal state (default true)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
}

func registerSnapshotReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_list",
		Description: "List snapshots for a sandbox.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotListInput) (*mcpsdk.CallToolResult, any, error) {
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
		Description: "Get a single snapshot by ID, including its state, label, and consistency mode.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotGetInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := opts.Client.GetSnapshot(ctx, in.SnapshotID)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}

// --- mutating tools ---

func registerSnapshotMutatingTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_create",
		Description: "Create a snapshot of a sandbox. By default waits for the snapshot to be in 'ready' state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotCreateInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.CreateSnapshot(ctx, in.SandboxID, client.CreateSnapshotRequest{
			Label:           in.Label,
			ConsistencyMode: in.ConsistencyMode,
		})
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, snapshotCreateDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return opts.Client.GetSnapshot(ctx, op.ResourceID)
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_restore",
		Description: "Restore a sandbox to a snapshot. The sandbox is rolled back to the snapshot's state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotRestoreInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.RestoreSnapshot(ctx, in.SnapshotID)
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, snapshotRestoreDefaultTimeout, opts.maxTimeout())
		// op.ResourceID on a restore op is the snapshot ID; the sandbox we
		// want to return is the one that was rolled back, addressed by op.SandboxID.
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return opts.Client.GetSandbox(ctx, op.SandboxID)
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "snapshot_delete",
		Description: "Delete a snapshot permanently. This cannot be undone.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in snapshotDeleteInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.DeleteSnapshot(ctx, in.SnapshotID)
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, snapshotDeleteDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return map[string]bool{"ok": true}, nil
		})
		return nil, res, err
	})
}
