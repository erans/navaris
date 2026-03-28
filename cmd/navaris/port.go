package main

import (
	"fmt"
	"strconv"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var portCmd = &cobra.Command{
	Use:   "port",
	Short: "Manage sandbox port bindings",
}

func init() {
	portCmd.AddCommand(portCreateCmd)
	portCmd.AddCommand(portListCmd)
	portCmd.AddCommand(portDeleteCmd)
}

var portCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Publish a port on a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")
		port, _ := cmd.Flags().GetInt("port")

		c := newClient(cmd)
		pb, err := c.CreatePort(cmd.Context(), sandboxID, client.CreatePortRequest{
			TargetPort: port,
		})
		if err != nil {
			return err
		}
		printResult(pb, []string{"SANDBOX_ID", "TARGET_PORT", "PUBLISHED_PORT", "HOST_ADDRESS"}, func() [][]string {
			return [][]string{{
				pb.SandboxID,
				strconv.Itoa(pb.TargetPort),
				strconv.Itoa(pb.PublishedPort),
				pb.HostAddress,
			}}
		})
		return nil
	},
}

var portListCmd = &cobra.Command{
	Use:   "list",
	Short: "List published ports for a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")

		c := newClient(cmd)
		ports, err := c.ListPorts(cmd.Context(), sandboxID)
		if err != nil {
			return err
		}
		printResult(ports, []string{"SANDBOX_ID", "TARGET_PORT", "PUBLISHED_PORT", "HOST_ADDRESS", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(ports))
			for i, p := range ports {
				rows[i] = []string{
					p.SandboxID,
					strconv.Itoa(p.TargetPort),
					strconv.Itoa(p.PublishedPort),
					p.HostAddress,
					p.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var portDeleteCmd = &cobra.Command{
	Use:   "delete <target-port>",
	Short: "Remove a published port from a sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")
		targetPort, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid target port %q: %w", args[0], err)
		}

		c := newClient(cmd)
		if err := c.DeletePort(cmd.Context(), sandboxID, targetPort); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Port %d deleted from sandbox %s\n", targetPort, sandboxID)
		return nil
	},
}

func init() {
	portCreateCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = portCreateCmd.MarkFlagRequired("sandbox")
	portCreateCmd.Flags().Int("port", 0, "Target port to publish (required)")
	_ = portCreateCmd.MarkFlagRequired("port")

	portListCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = portListCmd.MarkFlagRequired("sandbox")

	portDeleteCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = portDeleteCmd.MarkFlagRequired("sandbox")
}
