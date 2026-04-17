package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var sandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Manage sandboxes",
}

func init() {
	sandboxCmd.AddCommand(sandboxCreateCmd)
	sandboxCmd.AddCommand(sandboxListCmd)
	sandboxCmd.AddCommand(sandboxGetCmd)
	sandboxCmd.AddCommand(sandboxStartCmd)
	sandboxCmd.AddCommand(sandboxStopCmd)
	sandboxCmd.AddCommand(sandboxDestroyCmd)
	sandboxCmd.AddCommand(sandboxExecCmd)
	sandboxCmd.AddCommand(sandboxWaitStateCmd)

	sandboxCreateCmd.Flags().String("name", "", "Sandbox name")
	sandboxCreateCmd.Flags().String("image", "", "Image ID (required)")
	_ = sandboxCreateCmd.MarkFlagRequired("image")
	sandboxCreateCmd.Flags().Int("cpu", 0, "CPU limit")
	sandboxCreateCmd.Flags().Int("memory", 0, "Memory limit in MB")
	addWaitFlags(sandboxCreateCmd)

	sandboxStopCmd.Flags().Bool("force", false, "Force stop the sandbox")
	addWaitFlags(sandboxStartCmd)
	addWaitFlags(sandboxStopCmd)
	addWaitFlags(sandboxDestroyCmd)

	sandboxExecCmd.Flags().StringArray("env", nil, "Environment variable KEY=VAL (repeatable)")
	sandboxExecCmd.Flags().String("workdir", "", "Working directory inside the sandbox")
	sandboxExecCmd.Flags().Duration("timeout", 0, "Timeout for the command (e.g. 30s, 5m); 0 = no timeout")

	sandboxWaitStateCmd.Flags().String("state", "", "Target sandbox state (required) e.g. running, stopped, destroyed")
	sandboxWaitStateCmd.Flags().Duration("timeout", 60*time.Second, "Maximum time to wait")
	sandboxWaitStateCmd.Flags().Duration("interval", 500*time.Millisecond, "Polling interval")
	_ = sandboxWaitStateCmd.MarkFlagRequired("state")
}

var sandboxCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new sandbox from an image",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		image, _ := cmd.Flags().GetString("image")
		cpu, _ := cmd.Flags().GetInt("cpu")
		memory, _ := cmd.Flags().GetInt("memory")

		projectID := resolveProject(cmd)
		if projectID == "" {
			return fmt.Errorf("--project flag or NAVARIS_PROJECT env var is required")
		}

		req := client.CreateSandboxRequest{
			ProjectID: projectID,
			Name:      name,
			ImageID:   image,
		}
		if cmd.Flags().Changed("cpu") {
			req.CPULimit = &cpu
		}
		if cmd.Flags().Changed("memory") {
			req.MemoryLimitMB = &memory
		}

		c := newClient(cmd)
		op, err := c.CreateSandbox(cmd.Context(), req)
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, func() (any, error) {
			return c.GetSandbox(cmd.Context(), op.ResourceID)
		})
	},
}

var sandboxListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sandboxes for a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		projectID := resolveProject(cmd)
		if projectID == "" {
			return fmt.Errorf("--project flag or NAVARIS_PROJECT env var is required")
		}

		c := newClient(cmd)
		sandboxes, err := c.ListSandboxes(cmd.Context(), projectID, "")
		if err != nil {
			return err
		}
		printResult(sandboxes, []string{"SANDBOX_ID", "NAME", "STATE", "IMAGE", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(sandboxes))
			for i, s := range sandboxes {
				rows[i] = []string{
					s.SandboxID, s.Name, s.State, s.SourceImageID,
					s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var sandboxGetCmd = &cobra.Command{
	Use:   "get <sandbox-id>",
	Short: "Get a sandbox by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		s, err := c.GetSandbox(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printResult(s, []string{"SANDBOX_ID", "NAME", "STATE", "IMAGE", "PROJECT_ID", "CREATED_AT"}, func() [][]string {
			return [][]string{{
				s.SandboxID, s.Name, s.State, s.SourceImageID,
				s.ProjectID, s.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}}
		})
		return nil
	},
}

var sandboxStartCmd = &cobra.Command{
	Use:   "start <sandbox-id>",
	Short: "Start a stopped sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.StartSandbox(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, func() (any, error) {
			return c.GetSandbox(cmd.Context(), args[0])
		})
	},
}

var sandboxStopCmd = &cobra.Command{
	Use:   "stop <sandbox-id>",
	Short: "Stop a running sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		c := newClient(cmd)
		op, err := c.StopSandbox(cmd.Context(), args[0], client.StopSandboxRequest{Force: force})
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, func() (any, error) {
			return c.GetSandbox(cmd.Context(), args[0])
		})
	},
}

var sandboxDestroyCmd = &cobra.Command{
	Use:   "destroy <sandbox-id>",
	Short: "Destroy a sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.DestroySandbox(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, nil)
	},
}

var sandboxExecCmd = &cobra.Command{
	Use:   "exec <sandbox-id> -- <command...>",
	Short: "Execute a command in a sandbox",
	Args:  cobra.MinimumNArgs(1),
	// Disable flag parsing after the first positional arg so that
	// everything after -- is treated as the command.
	DisableFlagParsing: false,
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID := args[0]
		if len(args) < 2 {
			return fmt.Errorf("command is required after sandbox ID (use -- to separate)")
		}
		command := args[1:]

		envItems, _ := cmd.Flags().GetStringArray("env")
		envs, err := parseEnvFlags(envItems)
		if err != nil {
			return err
		}
		workDir, _ := cmd.Flags().GetString("workdir")
		timeout, _ := cmd.Flags().GetDuration("timeout")

		ctx := cmd.Context()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		c := newClient(cmd)
		resp, err := c.Exec(ctx, sandboxID, client.ExecRequest{
			Command: command,
			Env:     envs,
			WorkDir: workDir,
		})
		if err != nil {
			return err
		}

		if resp.Stdout != "" {
			fmt.Fprint(os.Stdout, resp.Stdout)
		}
		if resp.Stderr != "" {
			fmt.Fprint(os.Stderr, resp.Stderr)
		}

		if resp.ExitCode != 0 {
			os.Exit(resp.ExitCode)
		}
		return nil
	},
}

// PollSandboxState polls c.GetSandbox until the sandbox reaches the target
// state or the context expires; any GetSandbox error (including transient 404s)
// aborts immediately — retry policy is the caller's responsibility.
func PollSandboxState(ctx context.Context, c *client.Client, sandboxID, state string, interval time.Duration) (*client.Sandbox, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for {
		sbx, err := c.GetSandbox(ctx, sandboxID)
		if err != nil {
			return nil, err
		}
		if sbx.State == state {
			return sbx, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for sandbox %s to reach state %q: %w", sandboxID, state, ctx.Err())
		case <-time.After(interval):
		}
	}
}

var sandboxWaitStateCmd = &cobra.Command{
	Use:   "wait-state <sandbox-id>",
	Short: "Poll until a sandbox reaches a target state or the timeout expires",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		state, _ := cmd.Flags().GetString("state")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		interval, _ := cmd.Flags().GetDuration("interval")

		ctx := cmd.Context()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		c := newClient(cmd)
		sbx, err := PollSandboxState(ctx, c, args[0], state, interval)
		if err != nil {
			return err
		}
		if isQuiet() {
			printQuietIDs([]string{sbx.SandboxID})
			return nil
		}
		printResult(sbx, []string{"SANDBOX_ID", "STATE"}, func() [][]string {
			return [][]string{{sbx.SandboxID, sbx.State}}
		})
		return nil
	},
}

// parseEnvFlags converts ["KEY=value", ...] into a map. The first '=' is the
// separator so values can contain additional '=' characters. Returns an error
// for entries without '=' or with empty keys.
func parseEnvFlags(items []string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(items))
	for _, kv := range items {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			return nil, fmt.Errorf("--env %q: missing '=' separator", kv)
		}
		key := kv[:i]
		if key == "" {
			return nil, fmt.Errorf("--env %q: empty key", kv)
		}
		out[key] = kv[i+1:]
	}
	return out, nil
}
