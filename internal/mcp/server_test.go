package mcp_test

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/pkg/client"
)

// TestNewServer_ScaffoldHasNoToolsYet documents the M6.1 scaffold state:
// tool registration is stubbed. M7 fills in the read tools and this test's
// assertion must be inverted to "len > 0" then.
func TestNewServer_ScaffoldHasNoToolsYet(t *testing.T) {
	c := client.NewClient(client.WithURL("http://localhost:0"))
	s := internalmcp.NewServer(internalmcp.Options{Client: c})
	if s == nil {
		t.Fatal("expected non-nil server")
	}

	names := listToolNames(t, s)
	if len(names) != 0 {
		t.Fatalf("scaffold expected 0 tools; got %d (%v) — update this test in M7", len(names), names)
	}
}

func TestNewServer_ReadOnlyParityInScaffold(t *testing.T) {
	c := client.NewClient(client.WithURL("http://localhost:0"))
	full := internalmcp.NewServer(internalmcp.Options{Client: c, ReadOnly: false})
	ro := internalmcp.NewServer(internalmcp.Options{Client: c, ReadOnly: true})

	if got := len(listToolNames(t, full)); got != 0 {
		t.Errorf("scaffold full server: expected 0 tools, got %d", got)
	}
	if got := len(listToolNames(t, ro)); got != 0 {
		t.Errorf("scaffold read-only server: expected 0 tools, got %d", got)
	}
}

func listToolNames(t *testing.T, s *mcpsdk.Server) []string {
	t.Helper()
	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "x"}, nil)
	srvT, clientT := mcpsdk.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), srvT) }()
	sess, err := mc.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}
