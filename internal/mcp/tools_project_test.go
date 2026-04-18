package mcp_test

import (
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/testutil/apiserver"
	"github.com/navaris/navaris/pkg/client"
)

// startMCPTestServer starts an in-memory MCP server backed by a real navaris
// HTTP server. Returns the connected MCP client session and the underlying
// API URL so tests can also call the navaris client directly.
func startMCPTestServer(t *testing.T, readOnly bool) (*mcpsdk.ClientSession, string) {
	t.Helper()
	apiURL, _, _ := apiserver.New(t)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	srv := internalmcp.NewServer(internalmcp.Options{Client: c, ReadOnly: readOnly})

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil)
	srvT, clientT := mcpsdk.NewInMemoryTransports()
	go func() { _ = srv.Run(t.Context(), srvT) }()

	sess, err := mc.Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, apiURL
}

func decodeJSONResult(t *testing.T, res *mcpsdk.CallToolResult) any {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty result content")
	}
	textBlock, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var v any
	if err := json.Unmarshal([]byte(textBlock.Text), &v); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, textBlock.Text)
	}
	return v
}

func TestProjectList_Empty(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "project_list",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty list, got %d projects", len(arr))
	}
}

func TestProjectList_AfterCreate(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)

	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	if _, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "p1"}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "project_list",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeJSONResult(t, res)
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("expected array result, got %T", got)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 project, got %d", len(arr))
	}
}

func TestProjectGet_Found(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)

	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))
	created, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "p1"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "project_get",
		Arguments: map[string]any{"project_id": created.ProjectID},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected object result, got %T", got)
	}
	if gotID, _ := obj["ProjectID"].(string); gotID != created.ProjectID {
		t.Errorf("ProjectID: got %q, want %q", gotID, created.ProjectID)
	}
	if gotName, _ := obj["Name"].(string); gotName != created.Name {
		t.Errorf("Name: got %q, want %q", gotName, created.Name)
	}
}
