package mcp_test

import (
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

func TestOperationGet_Found(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "op-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "op-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "operation_get",
		Arguments: map[string]any{"operation_id": op.OperationID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", got)
	}
	if id, _ := obj["OperationID"].(string); id != op.OperationID {
		t.Errorf("expected OperationID=%q, got %q", op.OperationID, id)
	}
}

func TestOperationGet_NotFound(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "operation_get",
		Arguments: map[string]any{"operation_id": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for missing operation, got %v", res.Content)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(strings.ToLower(text), "not found") {
		t.Errorf("expected error text to mention 'not found', got %q", text)
	}
}

func TestOperationGet_Terminal(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "op-terminal"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "op-terminal-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	doneOp, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !doneOp.State.Terminal() {
		t.Fatalf("expected terminal state, got %q", doneOp.State)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "operation_get",
		Arguments: map[string]any{"operation_id": op.OperationID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", got)
	}
	state, ok := obj["State"].(string)
	if !ok {
		t.Fatalf("expected State string, got %T", obj["State"])
	}
	// State should round-trip the terminal value (succeeded|failed|cancelled).
	if client.OperationState(state) != doneOp.State {
		t.Errorf("expected State=%q, got %q", doneOp.State, state)
	}
}
