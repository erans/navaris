package main

import (
	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage snapshots",
}

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)
	snapshotCmd.AddCommand(snapshotGetCmd)
	snapshotCmd.AddCommand(snapshotRestoreCmd)
	snapshotCmd.AddCommand(snapshotDeleteCmd)
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a snapshot of a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")
		label, _ := cmd.Flags().GetString("label")
		consistency, _ := cmd.Flags().GetString("consistency")

		c := newClient(cmd)
		op, err := c.CreateSnapshot(cmd.Context(), sandboxID, client.CreateSnapshotRequest{
			Label:           label,
			ConsistencyMode: consistency,
		})
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, func() (any, error) {
			return c.GetSnapshot(cmd.Context(), op.ResourceID)
		})
	},
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List snapshots for a sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")

		c := newClient(cmd)
		snapshots, err := c.ListSnapshots(cmd.Context(), sandboxID)
		if err != nil {
			return err
		}
		printResult(snapshots, []string{"SNAPSHOT_ID", "LABEL", "STATE", "CONSISTENCY", "CREATED_AT"}, func() [][]string {
			rows := make([][]string, len(snapshots))
			for i, s := range snapshots {
				rows[i] = []string{
					s.SnapshotID, s.Label, s.State, s.ConsistencyMode,
					s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var snapshotGetCmd = &cobra.Command{
	Use:   "get <snapshot-id>",
	Short: "Get a snapshot by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		s, err := c.GetSnapshot(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		printResult(s, []string{"SNAPSHOT_ID", "SANDBOX_ID", "LABEL", "STATE", "CONSISTENCY", "CREATED_AT"}, func() [][]string {
			return [][]string{{
				s.SnapshotID, s.SandboxID, s.Label, s.State,
				s.ConsistencyMode, s.CreatedAt.Format("2006-01-02T15:04:05Z"),
			}}
		})
		return nil
	},
}

var snapshotRestoreCmd = &cobra.Command{
	Use:   "restore <snapshot-id>",
	Short: "Restore a sandbox to a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.RestoreSnapshot(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, nil)
	},
}

var snapshotDeleteCmd = &cobra.Command{
	Use:   "delete <snapshot-id>",
	Short: "Delete a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.DeleteSnapshot(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return handleOperation(cmd.Context(), c, cmd, op, nil)
	},
}

func init() {
	snapshotCreateCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = snapshotCreateCmd.MarkFlagRequired("sandbox")
	snapshotCreateCmd.Flags().String("label", "", "Snapshot label (required)")
	_ = snapshotCreateCmd.MarkFlagRequired("label")
	snapshotCreateCmd.Flags().String("consistency", "stopped", "Consistency mode (stopped, live)")
	addWaitFlags(snapshotCreateCmd)

	snapshotListCmd.Flags().String("sandbox", "", "Sandbox ID (required)")
	_ = snapshotListCmd.MarkFlagRequired("sandbox")

	addWaitFlags(snapshotRestoreCmd)
	addWaitFlags(snapshotDeleteCmd)
}
