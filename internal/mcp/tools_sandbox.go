package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

const (
	// sandboxCreateDefaultTimeout bounds how long sandbox_create waits for the
	// create+start operation to reach a terminal state before returning a
	// progress payload. Five minutes covers cold-start + image pull on slow backends.
	sandboxCreateDefaultTimeout = 5 * time.Minute
	// sandboxStartDefaultTimeout bounds sandbox_start waits. Two minutes is enough
	// for a warm start; cold-cache cases will fall through to a progress payload.
	sandboxStartDefaultTimeout = 2 * time.Minute
	// sandboxStopDefaultTimeout / sandboxDestroyDefaultTimeout bound stop/destroy
	// waits. Stop and destroy are fast on every backend; one minute is generous.
	sandboxStopDefaultTimeout    = time.Minute
	sandboxDestroyDefaultTimeout = time.Minute
	// sandboxExecDefaultTimeout is the default wall-clock budget for a one-shot
	// exec. Two minutes covers make build-class long commands; callers that need
	// tighter limits pass timeout_seconds explicitly.
	sandboxExecDefaultTimeout = 2 * time.Minute
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

// --- mutating tools ---

type sandboxCreateInput struct {
	ProjectID      string `json:"project_id" jsonschema:"ID of the project to create the sandbox in"`
	ImageID        string `json:"image_id" jsonschema:"ID of the base image to launch from"`
	Name           string `json:"name,omitempty" jsonschema:"optional human-readable name"`
	CPULimit       *int   `json:"cpu,omitempty" jsonschema:"optional vCPU limit"`
	MemoryLimitMB  *int   `json:"memory_mb,omitempty" jsonschema:"optional memory limit in MB"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait for the operation to reach terminal state (default true)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
}

// sandboxIDWaitInput is the input for sandbox_start and sandbox_destroy.
// Force is intentionally absent: those operations do not support forced
// termination, and advertising the field would mislead LLM agents.
type sandboxIDWaitInput struct {
	SandboxID      string `json:"sandbox_id" jsonschema:"ID of the sandbox"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait for the operation to reach terminal state (default true)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
}

// sandboxStopInput is the input for sandbox_stop. It adds Force on top of the
// common wait fields because force-stop is only meaningful for that operation.
type sandboxStopInput struct {
	SandboxID      string `json:"sandbox_id" jsonschema:"ID of the sandbox"`
	Wait           *bool  `json:"wait,omitempty" jsonschema:"wait for the operation to reach terminal state (default true)"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"max seconds to wait (default per-tool); ignored when wait=false"`
	Force          bool   `json:"force,omitempty" jsonschema:"force-stop the sandbox immediately"`
}

type sandboxExecInput struct {
	SandboxID      string            `json:"sandbox_id" jsonschema:"the running sandbox to exec in"`
	Command        []string          `json:"command" jsonschema:"argv to execute (no shell wrapping)"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"environment variables for the command"`
	WorkDir        string            `json:"work_dir,omitempty" jsonschema:"working directory inside the sandbox"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" jsonschema:"max seconds (default 120)"`
}

type sandboxExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func registerSandboxMutatingTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_create",
		Description: "Create a new sandbox from a base image. By default waits until the sandbox reaches the running state and returns the final sandbox object.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxCreateInput) (*mcpsdk.CallToolResult, any, error) {
		req := client.CreateSandboxRequest{
			ProjectID:     in.ProjectID,
			Name:          in.Name,
			ImageID:       in.ImageID,
			CPULimit:      in.CPULimit,
			MemoryLimitMB: in.MemoryLimitMB,
		}
		op, err := opts.Client.CreateSandbox(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, sandboxCreateDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return opts.Client.GetSandbox(ctx, op.ResourceID)
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_start",
		Description: "Start a stopped sandbox. By default waits until it reaches running state.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxIDWaitInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.StartSandbox(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, sandboxStartDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return opts.Client.GetSandbox(ctx, in.SandboxID)
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_stop",
		Description: "Stop a running sandbox. Set force=true for an immediate halt.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxStopInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.StopSandbox(ctx, in.SandboxID, client.StopSandboxRequest{Force: in.Force})
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, sandboxStopDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return opts.Client.GetSandbox(ctx, in.SandboxID)
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_destroy",
		Description: "Destroy a sandbox permanently. This cannot be undone.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxIDWaitInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.DestroySandbox(ctx, in.SandboxID)
		if err != nil {
			return nil, nil, err
		}
		if in.Wait != nil && !*in.Wait {
			return nil, op, nil
		}
		timeout := resolveTimeout(in.TimeoutSeconds, sandboxDestroyDefaultTimeout, opts.maxTimeout())
		res, err := waitForOpAndFetch(ctx, opts.Client, op, timeout, func() (any, error) {
			return map[string]bool{"ok": true}, nil
		})
		return nil, res, err
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "sandbox_exec",
		Description: "Execute a command synchronously inside a running sandbox. Returns stdout, stderr, and exit_code. Use for one-shot commands (build, test, inspect files). The sandbox must be in state 'running'. For stateful work across multiple commands (preserving cwd or env), create a tmux session via session_create.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in sandboxExecInput) (*mcpsdk.CallToolResult, *sandboxExecOutput, error) {
		timeout := resolveTimeout(in.TimeoutSeconds, sandboxExecDefaultTimeout, opts.maxTimeout())
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		resp, err := opts.Client.Exec(ctx, in.SandboxID, client.ExecRequest{
			Command: in.Command,
			Env:     in.Env,
			WorkDir: in.WorkDir,
		})
		if err != nil {
			return nil, nil, err
		}
		return nil, &sandboxExecOutput{ExitCode: resp.ExitCode, Stdout: resp.Stdout, Stderr: resp.Stderr}, nil
	})
}
