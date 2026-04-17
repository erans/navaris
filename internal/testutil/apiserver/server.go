// Package apiserver spins up an in-memory navaris API server for tests.
// It pairs a SQLite in-memory store, the mock provider, a real worker
// dispatcher, and the actual HTTP handlers — letting tests exercise the
// full HTTP path without a real provider backend.
package apiserver

import (
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

var dbCounter atomic.Int64

// New starts an in-memory navaris API server and returns its base URL plus
// the worker dispatcher (handy for callers that want to call WaitIdle).
// All resources are torn down via t.Cleanup.
func New(t *testing.T) (baseURL string, disp *worker.Dispatcher) {
	t.Helper()

	dsn := fmt.Sprintf("file:navtest%d?mode=memory&cache=shared", dbCounter.Add(1))
	store, err := sqlite.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mock := provider.NewMock()
	bus := eventbus.New(64)
	disp = worker.NewDispatcher(store.OperationStore(), bus, 4)
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
	go httpSrv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { httpSrv.Close() })

	return fmt.Sprintf("http://%s", ln.Addr().String()), disp
}
