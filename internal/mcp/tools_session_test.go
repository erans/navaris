package mcp_test

import (
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

func TestSessionList_AfterCreate(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "s-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "s-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateSession(t.Context(), op.ResourceID, client.CreateSessionRequest{Shell: "bash", Backing: "direct"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "session_list",
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
		t.Fatalf("expected 1 session, got %d", len(arr))
	}
	first, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("expected session object, got %T", arr[0])
	}
	if id, _ := first["SessionID"].(string); id != created.SessionID {
		t.Errorf("expected SessionID=%q, got %q", created.SessionID, id)
	}
}

func TestSessionGet_Found(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "sg-test"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "sg-sbx", ImageID: "mock-image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateSession(t.Context(), op.ResourceID, client.CreateSessionRequest{Shell: "bash", Backing: "direct"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "session_get",
		Arguments: map[string]any{"session_id": created.SessionID},
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
	if id, _ := obj["SessionID"].(string); id != created.SessionID {
		t.Errorf("expected SessionID=%q, got %q", created.SessionID, id)
	}
}

func TestSessionGet_NotFound(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "session_get",
		Arguments: map[string]any{"session_id": "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true for missing session, got %v", res.Content)
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
