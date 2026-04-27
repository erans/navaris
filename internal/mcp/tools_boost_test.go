package mcp_test

import (
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/navaris/navaris/pkg/client"
)

// boostTestSandbox creates a project + running sandbox with explicit CPU/memory
// limits and returns the sandbox ID. Centralizes the boilerplate the boost tool
// tests share. The explicit limits are required because Cancel needs to revert
// to "original" values — without them, the revert path tries to set nil limits
// and fails validation.
func boostTestSandbox(t *testing.T, c *client.Client) string {
	t.Helper()
	proj, err := c.CreateProject(t.Context(), client.CreateProjectRequest{Name: "b-test"})
	if err != nil {
		t.Fatal(err)
	}
	cpu, mem := 1, 256
	op, err := c.CreateSandbox(t.Context(), client.CreateSandboxRequest{
		ProjectID: proj.ProjectID, Name: "b-sbx", ImageID: "mock-image",
		CPULimit: &cpu, MemoryLimitMB: &mem,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WaitForOperation(t.Context(), op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second}); err != nil {
		t.Fatal(err)
	}
	return op.ResourceID
}

func TestBoostStart_AndGet(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	sandboxID := boostTestSandbox(t, c)

	// boost_start
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "boost_start",
		Arguments: map[string]any{
			"sandbox_id":       sandboxID,
			"cpu_limit":        4,
			"duration_seconds": 30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("boost_start error: %v", res.Content)
	}
	got := decodeJSONResult(t, res)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected boost object, got %T", got)
	}
	// client.Boost has snake_case JSON tags, so the MCP response uses them.
	if id, _ := m["boost_id"].(string); id == "" {
		t.Errorf("boost_start: missing boost_id in result: %v", m)
	}
	if state, _ := m["state"].(string); state != "active" {
		t.Errorf("boost_start: state = %v, want active", m["state"])
	}

	// boost_get
	res, err = sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "boost_get",
		Arguments: map[string]any{"sandbox_id": sandboxID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("boost_get error: %v", res.Content)
	}
	got = decodeJSONResult(t, res)
	m, ok = got.(map[string]any)
	if !ok {
		t.Fatalf("expected boost object from boost_get, got %T", got)
	}
	if sid, _ := m["sandbox_id"].(string); sid != sandboxID {
		t.Errorf("boost_get: sandbox_id = %q, want %q", sid, sandboxID)
	}
}

func TestBoostCancel(t *testing.T) {
	sess, apiURL := startMCPTestServer(t, false)
	c := client.NewClient(client.WithURL(apiURL), client.WithToken("test-token"))

	sandboxID := boostTestSandbox(t, c)

	cpu := 4
	if _, err := c.StartBoost(t.Context(), sandboxID, client.StartBoostRequest{
		CPULimit: &cpu, DurationSeconds: 30,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "boost_cancel",
		Arguments: map[string]any{"sandbox_id": sandboxID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("boost_cancel error: %v", res.Content)
	}

	// After cancel, GetBoost should 404.
	if _, err := c.GetBoost(t.Context(), sandboxID); err == nil {
		t.Error("expected GetBoost to fail after cancel")
	}
}

func TestBoostStart_PropagatesError(t *testing.T) {
	sess, _ := startMCPTestServer(t, false)
	res, err := sess.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "boost_start",
		Arguments: map[string]any{
			"sandbox_id":       "sbx-does-not-exist",
			"cpu_limit":        4,
			"duration_seconds": 30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected boost_start on missing sandbox to error")
	}
}

func TestBoostTools_HiddenInReadOnlyMode(t *testing.T) {
	sess, _ := startMCPTestServer(t, true)
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "boost_start" || tool.Name == "boost_cancel" {
			t.Errorf("read-only server should not expose %s", tool.Name)
		}
	}
	// boost_get is a read tool — it should still be there.
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "boost_get" {
			found = true
		}
	}
	if !found {
		t.Error("read-only server should still expose boost_get")
	}
}
