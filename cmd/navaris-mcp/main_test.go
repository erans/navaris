package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNavarisMCP_StdioToolsList(t *testing.T) {
	bin := buildBinary(t)

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env, "NAVARIS_API_URL=http://127.0.0.1:1") // port 1 is unreachable; tools/list is local
	tr := &mcpsdk.CommandTransport{Command: cmd}

	sess, err := mc.Connect(t.Context(), tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustHaveTool(t, tools.Tools, "sandbox_list")
	mustHaveTool(t, tools.Tools, "sandbox_create")
}

func TestNavarisMCP_StdioReadOnlyHidesMutatingTools(t *testing.T) {
	bin := buildBinary(t)

	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"NAVARIS_API_URL=http://127.0.0.1:1",
		"NAVARIS_MCP_READ_ONLY=true",
	)
	tr := &mcpsdk.CommandTransport{Command: cmd}

	sess, err := mc.Connect(t.Context(), tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustHaveTool(t, tools.Tools, "sandbox_list")
	mustNotHaveTool(t, tools.Tools, "sandbox_create")
}

func buildBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "navaris-mcp")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", out, ".")
	cmd.Dir = "."
	// Inherit GOPATH/module cache from test environment
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, b)
	}
	return out
}

func mustHaveTool(t *testing.T, tools []*mcpsdk.Tool, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return
		}
	}
	t.Errorf("expected tool %q in tool list", name)
}

func mustNotHaveTool(t *testing.T, tools []*mcpsdk.Tool, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			t.Errorf("tool %q should not be present in read-only tool list", name)
		}
	}
}
