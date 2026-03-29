package client_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
	"github.com/navaris/navaris/pkg/client"
)

var testDBCounter atomic.Int64

func startTestServer(t *testing.T) (string, *worker.Dispatcher) {
	t.Helper()

	dsn := fmt.Sprintf("file:clienttest%d?mode=memory&cache=shared", testDBCounter.Add(1))
	store, err := sqlite.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mock := provider.NewMock()
	bus := eventbus.New(64)
	disp := worker.NewDispatcher(store.OperationStore(), bus, 4)
	disp.Start()
	t.Cleanup(func() { disp.Stop() })

	projSvc := service.NewProjectService(store.ProjectStore())
	sbxSvc := service.NewSandboxService(
		store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
		store.SessionStore(), mock, bus, disp, "mock",
	)
	snapSvc := service.NewSnapshotService(
		store.SnapshotStore(), store.SandboxStore(), store.OperationStore(),
		mock, bus, disp,
	)
	imgSvc := service.NewImageService(
		store.ImageStore(), store.SnapshotStore(), store.OperationStore(),
		mock, bus, disp,
	)
	sessSvc := service.NewSessionService(
		store.SessionStore(), store.SandboxStore(), mock, bus,
	)
	opsSvc := service.NewOperationService(store.OperationStore(), disp)

	srv := api.NewServer(api.ServerConfig{
		Projects:   projSvc,
		Sandboxes:  sbxSvc,
		Snapshots:  snapSvc,
		Images:     imgSvc,
		Sessions:   sessSvc,
		Operations: opsSvc,
		Provider:   mock,
		Events:     bus,
		Ports:      store.PortBindingStore(),
		AuthToken:  "test-token",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })

	return fmt.Sprintf("http://%s", ln.Addr().String()), disp
}

func TestSDKIntegration(t *testing.T) {
	baseURL, disp := startTestServer(t)

	c := client.NewClient(
		client.WithURL(baseURL),
		client.WithToken("test-token"),
	)
	ctx := context.Background()

	// 1. Create a project
	proj, err := c.CreateProject(ctx, client.CreateProjectRequest{Name: "sdk-test-project"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ProjectID == "" {
		t.Fatal("expected non-empty ProjectID")
	}
	if proj.Name != "sdk-test-project" {
		t.Fatalf("expected name sdk-test-project, got %s", proj.Name)
	}

	// 2. Create a sandbox (returns operation)
	op, err := c.CreateSandbox(ctx, client.CreateSandboxRequest{
		ProjectID: proj.ProjectID,
		Name:      "sdk-test-sandbox",
		ImageID:   "image-1",
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if op.OperationID == "" {
		t.Fatal("expected non-empty OperationID")
	}
	sandboxID := op.ResourceID
	if sandboxID == "" {
		t.Fatal("expected non-empty ResourceID (sandbox ID)")
	}

	// 3. Wait for sandbox creation to complete
	// Let the dispatcher finish first to ensure the operation completes
	disp.WaitIdle()
	op, err = c.WaitForOperation(ctx, op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("WaitForOperation (create): %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("expected operation succeeded, got %s (error: %s)", op.State, op.ErrorText)
	}

	// 4. Get sandbox and verify it's running
	sbx, err := c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if sbx.State != "running" {
		t.Fatalf("expected sandbox state running, got %s", sbx.State)
	}
	if sbx.Name != "sdk-test-sandbox" {
		t.Fatalf("expected sandbox name sdk-test-sandbox, got %s", sbx.Name)
	}

	// 5. List sandboxes
	sandboxes, err := c.ListSandboxes(ctx, proj.ProjectID, "")
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(sandboxes) != 1 {
		t.Fatalf("expected 1 sandbox, got %d", len(sandboxes))
	}

	// 6. Stop sandbox and wait
	op, err = c.StopSandbox(ctx, sandboxID, client.StopSandboxRequest{Force: false})
	if err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
	disp.WaitIdle()
	op, err = c.WaitForOperation(ctx, op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("WaitForOperation (stop): %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("expected stop succeeded, got %s (error: %s)", op.State, op.ErrorText)
	}

	// Verify stopped state
	sbx, err = c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetSandbox after stop: %v", err)
	}
	if sbx.State != "stopped" {
		t.Fatalf("expected sandbox state stopped, got %s", sbx.State)
	}

	// 7. Destroy sandbox and wait
	op, err = c.DestroySandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("DestroySandbox: %v", err)
	}
	disp.WaitIdle()
	op, err = c.WaitForOperation(ctx, op.OperationID, &client.WaitOptions{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("WaitForOperation (destroy): %v", err)
	}
	if op.State != client.OpSucceeded {
		t.Fatalf("expected destroy succeeded, got %s (error: %s)", op.State, op.ErrorText)
	}

	// 8. Verify sandbox is destroyed
	sbx, err = c.GetSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("GetSandbox after destroy: %v", err)
	}
	if sbx.State != "destroyed" {
		t.Fatalf("expected sandbox state destroyed, got %s", sbx.State)
	}

	// 9. Test health endpoint
	health, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !health.Healthy {
		t.Fatalf("expected healthy=true, got false (error: %s)", health.Error)
	}

	// 10. Test auth failure with wrong token
	badClient := client.NewClient(
		client.WithURL(baseURL),
		client.WithToken("wrong-token"),
	)
	_, err = badClient.ListProjects(ctx)
	if err == nil {
		t.Fatal("expected error with wrong token")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *client.APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", apiErr.StatusCode)
	}
}
