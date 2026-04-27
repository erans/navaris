package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

type boostStartInput struct {
	SandboxID       string `json:"sandbox_id" jsonschema:"the sandbox to boost"`
	CPULimit        *int   `json:"cpu_limit,omitempty" jsonschema:"target vCPU count for the duration of the boost (omit to leave CPU unchanged)"`
	MemoryLimitMB   *int   `json:"memory_limit_mb,omitempty" jsonschema:"target memory in MB for the duration of the boost (omit to leave memory unchanged)"`
	DurationSeconds int    `json:"duration_seconds" jsonschema:"how long the boost stays active before the daemon auto-reverts; capped by --boost-max-duration"`
}

type boostIDInput struct {
	SandboxID string `json:"sandbox_id" jsonschema:"the boosted sandbox's ID"`
}

// registerBoostReadTools registers boost_get. Boost is a runtime construct
// (one active boost per sandbox), not a separate addressable resource — there
// is no boost_list because boosts are accessed by sandbox ID.
func registerBoostReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "boost_get",
		Description: "Get the active boost for a sandbox, including original/boosted CPU and memory limits, expiry, and source (external/in_sandbox). Returns 404 if no boost is active.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in boostIDInput) (*mcpsdk.CallToolResult, any, error) {
		b, err := opts.Client.GetBoost(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		return nil, b, nil
	})
}

// registerBoostMutatingTools registers boost_start and boost_cancel.
// Both are synchronous (no wait/timeout) because boost lifecycle changes are
// near-instantaneous on the daemon side: boost_start reconfigures the running
// sandbox via the existing resize machinery and returns immediately; cancel
// reverts in-line. If either becomes long-running on a backend, promote to
// the wait/timeout pattern used by sandbox_create.
func registerBoostMutatingTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "boost_start",
		Description: "Start a time-bounded boost on a sandbox: temporarily raise CPU and/or memory limits for duration_seconds; the daemon auto-reverts at expiry. Set at least one of cpu_limit / memory_limit_mb. Replaces any active boost on the same sandbox.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in boostStartInput) (*mcpsdk.CallToolResult, any, error) {
		b, err := opts.Client.StartBoost(ctx, in.SandboxID, client.StartBoostRequest{
			CPULimit:        in.CPULimit,
			MemoryLimitMB:   in.MemoryLimitMB,
			DurationSeconds: in.DurationSeconds,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, b, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "boost_cancel",
		Description: "Cancel the active boost on a sandbox immediately, reverting to the original CPU/memory limits before duration_seconds elapses. No-op (404) if no boost is active.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in boostIDInput) (*mcpsdk.CallToolResult, any, error) {
		if err := opts.Client.CancelBoost(ctx, in.SandboxID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"sandbox_id": in.SandboxID, "status": "cancelled"}, nil
	})
}
