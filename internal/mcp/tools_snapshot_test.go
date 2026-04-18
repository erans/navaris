package mcp_test

import (
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

func TestSnapshotList_AfterCreate(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "snap-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "snap-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	snapOp, err := c.CreateSnapshot(t.Context(), op.ResourceID, client.CreateSnapshotRequest{
		Label: "test",
		// "live" required: default "stopped" mode is rejected by the service while the sandbox is running.
		ConsistencyMode: "live",
	})
	if err != nil {
		t.Fatal(err)
	}
	doneOp, err := c.WaitForOperation(t.Context(), snapOp.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if doneOp.SnapshotID == "" {
		t.Fatal("operation did not record SnapshotID")
	}
	wantSnapshotID := doneOp.SnapshotID

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "snapshot_list",
		Arguments: map[string]any{"sandbox_id": op.ResourceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(arr))
	}
	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected snapshot object, got %T", arr[0])
	}
	if id, _ := first["SnapshotID"].(string); id != wantSnapshotID {
		t.Errorf("expected SnapshotID=%q, got %q", wantSnapshotID, id)
	}
}

func TestSnapshotGet_Found(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "snap-get"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "snap-get-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	snapOp, err := c.CreateSnapshot(t.Context(), op.ResourceID, client.CreateSnapshotRequest{
		Label: "test",
		// "live" required: default "stopped" mode is rejected by the service while the sandbox is running.
		ConsistencyMode: "live",
	})
	if err != nil {
		t.Fatal(err)
	}
	doneOp, err := c.WaitForOperation(t.Context(), snapOp.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if doneOp.SnapshotID == "" {
		t.Fatal("operation did not record SnapshotID")
	}
	wantSnapshotID := doneOp.SnapshotID

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "snapshot_get",
		Arguments: map[string]any{"snapshot_id": wantSnapshotID},
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
	if id, _ := obj["SnapshotID"].(string); id != wantSnapshotID {
		t.Errorf("expected SnapshotID=%q, got %q", wantSnapshotID, id)
	}
}

func TestSnapshotGet_NotFound(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "snapshot_get",
		Arguments: map[string]any{"snapshot_id": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for missing snapshot, got %v", res.Content)
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
