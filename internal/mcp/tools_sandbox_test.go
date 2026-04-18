package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

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
