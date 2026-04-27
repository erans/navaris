// Package apiserver spins up an in-memory navaris API server for tests.
// It pairs a SQLite in-memory store, the mock provider, a real worker
// dispatcher, and the actual HTTP handlers — letting tests exercise the
// full HTTP path without a real provider backend.
package apiserver

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
	internalmcp "github.com/navaris/navaris/internal/mcp"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

var dbCounter atomic.Int64

// Option configures the apiserver helper.
type Option func(*config)

type config struct {
	mcp         bool
	mcpReadOnly bool
}

// WithMCP mounts the embedded MCP handler at /v1/mcp using the same auth token
// that the apiserver requires. Pass readOnly=true to hide mutating tools.
func WithMCP(readOnly bool) Option {
	return func(c *config) {
		c.mcp = true
		c.mcpReadOnly = readOnly
	}
}

// New starts an in-memory navaris API server and returns its base URL, the
// worker dispatcher (handy for callers that want to call WaitIdle), and the
// mock provider so callers can override individual provider functions.
// All resources are torn down via t.Cleanup.
//
// Optional options enable additional features such as the embedded MCP handler.
// Existing callers that pass no options get unchanged behavior.
func New(t *testing.T, opts ...Option) (string, *worker.Dispatcher, *provider.MockProvider) {
	t.Helper()

	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	dsn := fmt.Sprintf("file:navtest%d?mode=memory&cache=shared", dbCounter.Add(1))
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
		store.SessionStore(), mock, bus, disp, "mock", false,
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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srvCfg := api.ServerConfig{
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
	}

	if cfg.mcp {
		// Mirror navarisd's own bootstrap: create a "default" project so
		// mutating-tool tests can target it without having to wire up a project.
		// Only done when MCP is enabled; plain callers may be testing project
		// creation themselves and must start with an empty DB.
		if _, err := projSvc.Create(context.Background(), "default", nil); err != nil {
			t.Fatalf("create default project: %v", err)
		}

		mcpHandler := internalmcp.NewHTTPHandler(internalmcp.HTTPOptions{
			LocalAPIURL: fmt.Sprintf("http://%s", ln.Addr().String()),
			AuthToken:   "test-token",
			ReadOnly:    cfg.mcpReadOnly,
			MaxTimeout:  10 * time.Minute,
		})
		srvCfg.MCPHandler = mcpHandler
		srvCfg.MCPPath = "/v1/mcp"
	}

	srv := api.NewServer(srvCfg)

	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { httpSrv.Close() })

	return fmt.Sprintf("http://%s", ln.Addr().String()), disp, mock
}
