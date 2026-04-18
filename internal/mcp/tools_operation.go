package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type operationGetInput struct {
	OperationID string `json:"operation_id" jsonschema:"ID of the operation to fetch"`
}

type operationCancelInput struct {
	OperationID string `json:"operation_id" jsonschema:"the operation's ID"`
}

func registerOperationReadTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "operation_get",
		Description: "Get the current state of an asynchronous operation by its ID. Operations are returned by sandbox and snapshot lifecycle tools; use this to check whether they have reached a terminal state (succeeded, failed, or cancelled).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in operationGetInput) (*mcpsdk.CallToolResult, any, error) {
		op, err := opts.Client.GetOperation(ctx, in.OperationID)
		if err != nil {
			return nil, nil, err
		}
		return nil, op, nil
	})
}

func registerOperationMutatingTools(s *mcpsdk.Server, opts Options) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "operation_cancel",
		Description: "Cancel a pending or running operation. Cancelling an already-terminal operation is a no-op and still returns ok; cancelling an unknown operation ID returns an error.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in operationCancelInput) (*mcpsdk.CallToolResult, map[string]bool, error) {
		if err := opts.Client.CancelOperation(ctx, in.OperationID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]bool{"ok": true}, nil
	})
}
