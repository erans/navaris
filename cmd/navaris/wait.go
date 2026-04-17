package main

import (
	"context"
	"fmt"
	"time"

	"github.com/navaris/navaris/pkg/client"
	"github.com/spf13/cobra"
)

// addWaitFlags adds --wait and --timeout flags to a command.
func addWaitFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("wait", false, "Wait for the operation to complete")
	cmd.Flags().Duration("timeout", client.DefaultWaitTimeout, "Timeout when waiting for an operation")
}

// handleOperation prints the operation and, when --wait is set, polls until
// the operation reaches a terminal state. If resourceFetchFn is provided and
// the operation succeeds, the fetched resource is printed instead of the
// final operation.
func handleOperation(
	ctx context.Context,
	c *client.Client,
	cmd *cobra.Command,
	op *client.Operation,
	resourceFetchFn func() (any, error),
) error {
	wait, _ := cmd.Flags().GetBool("wait")
	if !wait {
		if isQuiet() && !isJSONOutput() {
			printQuietIDs([]string{op.OperationID})
			return nil
		}
		printResult(op, []string{"OPERATION", "TYPE", "STATE", "RESOURCE"}, func() [][]string {
			return [][]string{{op.OperationID, op.Type, string(op.State), op.ResourceID}}
		})
		return nil
	}

	timeout, _ := cmd.Flags().GetDuration("timeout")
	opts := &client.WaitOptions{Timeout: timeout}

	final, err := c.WaitForOperation(ctx, op.OperationID, opts)
	if err != nil {
		return fmt.Errorf("waiting for operation: %w", err)
	}

	if final.State == client.OpFailed {
		return fmt.Errorf("operation %s failed: %s", final.OperationID, final.ErrorText)
	}
	if final.State == client.OpCancelled {
		return fmt.Errorf("operation %s was cancelled", final.OperationID)
	}

	// If we have a way to fetch the resulting resource, print that instead.
	if resourceFetchFn != nil {
		res, err := resourceFetchFn()
		if err != nil {
			// Fall through to printing the operation if fetch fails.
			printJSON(final)
			return nil
		}
		if isQuiet() && !isJSONOutput() {
			if id := resourceID(res); id != "" {
				printQuietIDs([]string{id})
				return nil
			}
		}
		printJSON(res)
		return nil
	}

	printResult(final, []string{"OPERATION", "TYPE", "STATE", "RESOURCE"}, func() [][]string {
		fin := "-"
		if final.FinishedAt != nil {
			fin = final.FinishedAt.Format(time.RFC3339)
		}
		return [][]string{{final.OperationID, final.Type, string(final.State), fin}}
	})
	return nil
}

// resourceID extracts the primary ID field from common resource types using
// type assertions. Returns empty string for unknown types.
func resourceID(v any) string {
	switch r := v.(type) {
	case *client.Sandbox:
		return r.SandboxID
	case *client.Session:
		return r.SessionID
	case *client.Snapshot:
		return r.SnapshotID
	case *client.BaseImage:
		return r.ImageID
	case *client.Project:
		return r.ProjectID
	case *client.Operation:
		return r.OperationID
	}
	return ""
}
