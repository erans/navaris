package mcp_test

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/pkg/client"
)

// TestNewServer_RegistersTools confirms tool registration wires through.
// Originally TestNewServer_ScaffoldHasNoToolsYet (M6.1 scaffold), inverted
// in M7.1 once project tools landed.
func TestNewServer_RegistersTools(t *testing.T) {
	c := client.NewClient(client.WithURL("http://localhost:0"))
	s := internalmcp.NewServer(internalmcp.Options{Client: c})
	if s == nil {
		t.Fatal("expected non-nil server")
	}

	names := listToolNames(t, s)
	if len(names) == 0 {
		t.Fatal("expected at least one registered tool")
	}
}

func TestNewServer_PanicsWithoutClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Options.Client is nil")
		}
	}()
	_ = internalmcp.NewServer(internalmcp.Options{})
}

// TestNewServer_ReadOnlyExposesNoMutators asserts that the read-only tool
// set is a strict subset of the full tool set. In M7 milestones (read tools
// only) the two sets are equal; once mutating tools land in M8 the read-only
// set will be strictly smaller.
func TestNewServer_ReadOnlyExposesNoMutators(t *testing.T) {
	c := client.NewClient(client.WithURL("http://localhost:0"))
	full := internalmcp.NewServer(internalmcp.Options{Client: c, ReadOnly: false})
	ro := internalmcp.NewServer(internalmcp.Options{Client: c, ReadOnly: true})

	fullNames := listToolNames(t, full)
	roNames := listToolNames(t, ro)

	if len(roNames) > len(fullNames) {
		t.Fatalf("read-only set (%d) larger than full set (%d)", len(roNames), len(fullNames))
	}

	fullSet := make(map[string]struct{}, len(fullNames))
	for _, n := range fullNames {
		fullSet[n] = struct{}{}
	}
	for _, n := range roNames {
		if _, ok := fullSet[n]; !ok {
			t.Errorf("read-only tool %q is not in the full tool set", n)
		}
	}
}

func listToolNames(t *testing.T, s *mcpsdk.Server) []string {
	t.Helper()
	mc := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "x"}, nil)
	srvT, clientT := mcpsdk.NewInMemoryTransports()
	go func() { _ = s.Run(t.Context(), srvT) }()
	sess, err := mc.Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	return names
}
