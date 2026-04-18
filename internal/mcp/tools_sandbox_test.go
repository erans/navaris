package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/pkg/client"
)

func TestSandboxList_FiltersByProject(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	projA, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "sb-test-a"})
	if err != nil {
		t.Fatal(err)
	}
	projB, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "sb-test-b"})
	if err != nil {
		t.Fatal(err)
	}

	opA, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: projA.ProjectID, Name: "sbx-a", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	opB, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: projB.ProjectID, Name: "sbx-b", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	doneA, err := c.WaitForOperation(t.Context(), opA.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), opB.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_list",
		Arguments: map[string]any{"project_id": projA.ProjectID},
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
		t.Fatalf("expected 1 sandbox in project A, got %d", len(arr))
	}
	obj, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected sandbox object, got %T", arr[0])
	}
	wantID := doneA.ResourceID
	if gotID, _ := obj["SandboxID"].(string); gotID != wantID {
		t.Errorf("SandboxID: got %q, want %q", gotID, wantID)
	}
}

func TestSandboxList_StateFilter(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "sb-state"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "sbx-state", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_list",
		Arguments: map[string]any{"project_id": proj.ProjectID, "state": "running"},
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
		t.Fatalf("expected 1 running sandbox, got %d", len(arr))
	}

	res, err = sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_list",
		Arguments: map[string]any{"project_id": proj.ProjectID, "state": "stopped"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got = decodeJSONResult(t, res)
	arr, ok = got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 0 {
		t.Errorf("expected 0 stopped sandboxes, got %d", len(arr))
	}
}

func TestSandboxGet_NotFound(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "sandbox_get",
		Arguments: map[string]any{"sandbox_id": "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		body, _ := json.Marshal(res)
		t.Fatalf("expected IsError=true for missing sandbox, got %s", body)
	}
	// Also verify it's specifically a not-found error.
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

// --- mutating tools ---

func TestSandboxCreate_WaitTrue_ReturnsRunningSandbox(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "create-test"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_create",
		Arguments: map[string]any{
			"project_id":      proj.ProjectID,
			"image_id":        "mock-image",
			"name":            "create-target",
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
	if got["State"] != "running" {
		t.Errorf("expected state=running, got %v (full: %v)", got["State"], got)
	}
	if gotName, _ := got["Name"].(string); gotName != "create-target" {
		t.Errorf("expected Name=create-target, got %q (full: %v)", gotName, got)
	}
	if gotProj, _ := got["ProjectID"].(string); gotProj != proj.ProjectID {
		t.Errorf("expected ProjectID=%q, got %q", proj.ProjectID, gotProj)
	}
}

func TestSandboxCreate_WaitFalse_ReturnsOperation(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "create-test2"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_create",
		Arguments: map[string]any{
			"project_id": proj.ProjectID,
			"image_id":   "mock-image",
			"name":       "create-target2",
			"wait":       false,
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
		t.Errorf("expected OperationID, got %v", got)
	}
}

func TestSandboxDestroy_RemovesSandbox(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "destroy-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "destroy-target", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_destroy",
		Arguments: map[string]any{
			"sandbox_id": op.ResourceID,
			"wait":       true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("destroy tool error: %v", res.Content)
	}
	got, ok := decodeJSONResult(t, res).(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", decodeJSONResult(t, res))
	}
	if okFlag, _ := got["ok"].(bool); !okFlag {
		t.Errorf("expected ok=true, got %v", got)
	}
}

// TestSandboxCreate_FailedOp_SurfacesError verifies that when the underlying
// provider's CreateSandbox returns an error, the resulting failed operation
// surfaces as an MCP tool error when the caller waits for completion.
//
// The provider error happens asynchronously inside the operation worker, not
// during the synchronous client.CreateSandbox HTTP call (which only enqueues
// the operation). So sandbox_create itself succeeds; the wait then observes
// a failed operation and waitForOpAndFetch returns an error.
func TestSandboxCreate_FailedOp_SurfacesError(t *testing.T) {
	sess, apiURL, mock := startMCPTestServerWithMock(t, false)
	mock.CreateSandboxFn = func(_ context.Context, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
		return domain.BackendRef{}, errors.New("simulated provider failure")
	}
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "fail-test"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_create",
		Arguments: map[string]any{
			"project_id":      proj.ProjectID,
			"image_id":        "mock-image",
			"name":            "fail-target",
			"wait":            true,
			"timeout_seconds": 10,
		},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		body, _ := json.Marshal(res)
		t.Fatalf("expected IsError=true when underlying op fails, got %s", body)
	}
}

func TestSandboxStart_WaitFalse(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "start-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "start-target", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	stopOp, err := c.StopSandbox(t.Context(), op.ResourceID, client.StopSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), stopOp.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_start",
		Arguments: map[string]any{
			"sandbox_id": op.ResourceID,
			"wait":       false,
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
		t.Errorf("expected OperationID, got %v", got)
	}
}

func TestSandboxStop_WaitTrue(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "stop-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "stop-target", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_stop",
		Arguments: map[string]any{
			"sandbox_id":      op.ResourceID,
			"wait":            true,
			"timeout_seconds": 10,
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
	if gotID, _ := got["SandboxID"].(string); gotID != op.ResourceID {
		t.Errorf("SandboxID: got %q, want %q", gotID, op.ResourceID)
	}
	if got["State"] != "stopped" {
		t.Errorf("expected State=stopped, got %v (full: %v)", got["State"], got)
	}
}

func TestSandboxExec_RunsCommand(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "exec-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "exec-target", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "sandbox_exec",
		Arguments: map[string]any{
			"sandbox_id": op.ResourceID,
			"command":    []string{"echo", "hello"},
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
	if _, ok := got["exit_code"]; !ok {
		t.Errorf("missing exit_code in %v", got)
	}
}

// TestNewServer_ReadOnly_HidesMutatingTools verifies the read-only mode hides
// every mutating tool registered across the package. Add new mutators to the
// hidden-name slice below as they land.
func TestNewServer_ReadOnly_HidesMutatingTools(t *testing.T) {
	roSess, _ := startMCPTestServer(t, true)
	tools, err := roSess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	roSet := make(map[string]struct{}, len(tools.Tools))
	for _, tl := range tools.Tools {
		roSet[tl.Name] = struct{}{}
	}
	for _, name := range []string{"sandbox_create", "sandbox_start", "sandbox_stop", "sandbox_destroy", "sandbox_exec", "session_create", "session_destroy", "snapshot_create", "snapshot_restore", "snapshot_delete", "operation_cancel"} {
		if _, ok := roSet[name]; ok {
			t.Errorf("mutating tool %q should be hidden in ReadOnly mode", name)
		}
	}
	for _, name := range []string{"sandbox_list", "sandbox_get"} {
		if _, ok := roSet[name]; !ok {
			t.Errorf("read-only tool %q should be present in ReadOnly mode", name)
		}
	}
}
