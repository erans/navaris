package mcp_test

import (
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

// --- mutating tools ---

// createRunningSnapshot is a test helper that creates a project, a running
// sandbox, and a snapshot via the snapshot_create tool. It returns the MCP
// session, the API client, the sandbox ID, and the snapshot ID decoded from
// the tool result.
func createRunningSnapshot(t *testing.T, sess *mcpsdk.ClientSession, apiURL string, label string) (sandboxID string, snapshotID string) {
	t.Helper()
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "snap-mut-" + label})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "snap-sbx-" + label, ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	sandboxID = op.ResourceID

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "snapshot_create",
		Arguments: map[string]any{
			"sandbox_id":       sandboxID,
			"label":            label,
			"consistency_mode": "live",
			"wait":             true,
			"timeout_seconds":  30,
		},
	})
	if err != nil {
		t.Fatalf("snapshot_create call: %v", err)
	}
	if res.IsError {
		t.Fatalf("snapshot_create tool error: %v", res.Content)
	}
	obj, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	snapshotID, _ = obj["SnapshotID"].(string)
	if snapshotID == "" {
		t.Fatalf("expected non-empty SnapshotID in snapshot_create result, got %v", obj)
	}
	return sandboxID, snapshotID
}

func TestSnapshotCreate_WaitTrue(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "snap-create-wait"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "snap-create-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "snapshot_create",
		Arguments: map[string]any{
			"sandbox_id":       op.ResourceID,
			"label":            "test-label",
			"consistency_mode": "live",
			"wait":             true,
			"timeout_seconds":  30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	snapID, _ := got["SnapshotID"].(string)
	if snapID == "" {
		t.Errorf("expected non-empty SnapshotID, got %v", got)
	}
	if got["State"] != "ready" {
		t.Errorf("expected State=ready, got %v (full: %v)", got["State"], got)
	}
}

func TestSnapshotCreate_WaitFalse_ReturnsOperation(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "snap-create-nowait"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "snap-create-nowait-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "snapshot_create",
		Arguments: map[string]any{
			"sandbox_id":       op.ResourceID,
			"label":            "nowait-label",
			"consistency_mode": "live",
			"wait":             false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	opID, _ := got["OperationID"].(string)
	if opID == "" {
		t.Errorf("expected non-empty OperationID, got %v", got)
	}
}

func TestSnapshotRestore_WaitTrue(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	sandboxID, snapshotID := createRunningSnapshot(t, sess, apiURL, "restore-test")

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "snapshot_restore",
		Arguments: map[string]any{
			"snapshot_id":     snapshotID,
			"wait":            true,
			"timeout_seconds": 30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	if gotID, _ := got["SandboxID"].(string); gotID != sandboxID {
		t.Errorf("expected SandboxID=%q, got %q (full: %v)", sandboxID, gotID, got)
	}
	if got["State"] != "running" {
		t.Errorf("expected State=running, got %v (full: %v)", got["State"], got)
	}
}

func TestSnapshotDelete_WaitTrue(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	_, snapshotID := createRunningSnapshot(t, sess, apiURL, "delete-test")

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "snapshot_delete",
		Arguments: map[string]any{
			"snapshot_id":     snapshotID,
			"wait":            true,
			"timeout_seconds": 30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	if okFlag, _ := got["ok"].(bool); !okFlag {
		t.Errorf("expected ok=true, got %v", got)
	}
}

func TestSnapshotDelete_NotFound_PropagatesError(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "snapshot_delete",
		Arguments: map[string]any{"snapshot_id": "snap-nonexistent"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		body := ""
		for _, c := range res.Content {
			if tc, ok := c.(*mcpsdk.TextContent); ok {
				body += tc.Text
			}
		}
		t.Fatalf("expected IsError=true for nonexistent snapshot, got: %s", body)
	}
}

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
