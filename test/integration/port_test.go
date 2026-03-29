//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/navaris/navaris/pkg/client"
)

func TestPortPublishListDelete(t *testing.T) {
	c := newClient()
	ctx := context.Background()

	proj := createTestProject(t, c)
	sandboxID := createTestSandbox(t, c, proj.ProjectID, "port-test-sbx")

	pb, err := c.CreatePort(ctx, sandboxID, client.CreatePortRequest{
		TargetPort: 8080,
	})
	if err != nil {
		t.Fatalf("create port: %v", err)
	}
	t.Logf("published port %d -> %d", pb.TargetPort, pb.PublishedPort)

	if pb.TargetPort != 8080 {
		t.Fatalf("expected target port 8080, got %d", pb.TargetPort)
	}
	if pb.PublishedPort == 0 {
		t.Fatal("expected non-zero published port")
	}

	ports, err := c.ListPorts(ctx, sandboxID)
	if err != nil {
		t.Fatalf("list ports: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(ports))
	}
	if ports[0].TargetPort != 8080 {
		t.Fatalf("listed port target mismatch: %d", ports[0].TargetPort)
	}

	if err := c.DeletePort(ctx, sandboxID, 8080); err != nil {
		t.Fatalf("delete port: %v", err)
	}

	ports, err = c.ListPorts(ctx, sandboxID)
	if err != nil {
		t.Fatalf("list ports after delete: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports after delete, got %d", len(ports))
	}
}
