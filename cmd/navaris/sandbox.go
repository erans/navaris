package main

import (
	"fmt"
	"os"

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

		c := newClient(cmd)
		resp, err := c.Exec(cmd.Context(), sandboxID, client.ExecRequest{Command: command})
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

func init() {
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
}
