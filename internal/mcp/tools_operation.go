package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type operationGetInput struct {
	OperationID string `json:"operation_id" jsonschema:"ID of the operation to fetch"`
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
