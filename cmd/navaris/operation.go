package main

import (
	"fmt"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

var operationCmd = &cobra.Command{
	Use:   "operation",
	Short: "Manage asynchronous operations",
}

func init() {
	operationCmd.AddCommand(operationListCmd, operationGetCmd, operationCancelCmd, operationWaitCmd)

	operationListCmd.Flags().String("sandbox", "", "Filter by sandbox ID")
	operationListCmd.Flags().String("state", "", "Filter by state (pending, running, succeeded, failed, cancelled)")

	operationWaitCmd.Flags().Duration("timeout", client.DefaultWaitTimeout, "Maximum time to wait for terminal state")
}

var operationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List operations",
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxID, _ := cmd.Flags().GetString("sandbox")
		state, _ := cmd.Flags().GetString("state")

		c := newClient(cmd)
		ops, err := c.ListOperations(cmd.Context(), sandboxID, state)
		if err != nil {
			return err
		}
		printResult(ops, []string{"OPERATION_ID", "TYPE", "STATE", "RESOURCE", "STARTED_AT"}, func() [][]string {
			rows := make([][]string, len(ops))
			for i, op := range ops {
				rows[i] = []string{
					op.OperationID, op.Type, string(op.State), op.ResourceID,
					op.StartedAt.Format("2006-01-02T15:04:05Z"),
				}
			}
			return rows
		})
		return nil
	},
}

var operationGetCmd = &cobra.Command{
	Use:   "get <operation-id>",
	Short: "Get an operation by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		op, err := c.GetOperation(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fin := "-"
		if op.FinishedAt != nil {
			fin = op.FinishedAt.Format(time.RFC3339)
		}
		errText := "-"
		if op.ErrorText != "" {
			errText = op.ErrorText
		}
		printResult(op, []string{"OPERATION_ID", "TYPE", "STATE", "RESOURCE", "STARTED_AT", "FINISHED_AT", "ERROR"}, func() [][]string {
			return [][]string{{
				op.OperationID, op.Type, string(op.State), op.ResourceID,
				op.StartedAt.Format(time.RFC3339), fin, errText,
			}}
		})
		return nil
	},
}

var operationCancelCmd = &cobra.Command{
	Use:   "cancel <operation-id>",
	Short: "Cancel a pending or running operation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newClient(cmd)
		if err := c.CancelOperation(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Operation %s cancelled\n", args[0])
		return nil
	},
}

var operationWaitCmd = &cobra.Command{
	Use:   "wait <operation-id>",
	Short: "Wait for an operation to reach a terminal state",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		c := newClient(cmd)
		op, err := c.WaitForOperation(cmd.Context(), args[0], &client.WaitOptions{Timeout: timeout})
		if err != nil {
			return err
		}
		switch op.State {
		case client.OpFailed:
			return fmt.Errorf("operation %s failed: %s", op.OperationID, op.ErrorText)
		case client.OpCancelled:
			return fmt.Errorf("operation %s was cancelled", op.OperationID)
		}
		if isQuiet() && !isJSONOutput() {
			id := op.ResourceID
			if id == "" {
				id = op.OperationID
			}
			printQuietIDs([]string{id})
			return nil
		}
		fin := "-"
		if op.FinishedAt != nil {
			fin = op.FinishedAt.Format(time.RFC3339)
		}
		printResult(op, []string{"OPERATION_ID", "TYPE", "STATE", "RESOURCE", "FINISHED_AT"}, func() [][]string {
			return [][]string{{op.OperationID, op.Type, string(op.State), op.ResourceID, fin}}
		})
		return nil
	},
}
