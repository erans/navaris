package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

var testDBCounter atomic.Int64

type testEnv struct {
	handler    http.Handler
	store      *sqlite.Store
	mock       *provider.MockProvider
	events     *eventbus.MemoryBus
	dispatcher *worker.Dispatcher
	projects   *service.ProjectService
	sandboxes  *service.SandboxService
	snapshots  *service.SnapshotService
	images     *service.ImageService
	sessions   *service.SessionService
	operations *service.OperationService
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:testdb%d?mode=memory&cache=shared", testDBCounter.Add(1))
	s, err := sqlite.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	mock := provider.NewMock()
	bus := eventbus.New(64)
	disp := worker.NewDispatcher(s.OperationStore(), bus, 4)
	disp.Start()
	t.Cleanup(func() { disp.Stop() })

	projSvc := service.NewProjectService(s.ProjectStore())
	sbxSvc := service.NewSandboxService(
		s.SandboxStore(), s.SnapshotStore(), s.OperationStore(), s.PortBindingStore(),
		s.SessionStore(), mock, bus, disp, "mock",
	)
	snapSvc := service.NewSnapshotService(
		s.SnapshotStore(), s.SandboxStore(), s.OperationStore(),
		mock, bus, disp,
	)
	imgSvc := service.NewImageService(
		s.ImageStore(), s.SnapshotStore(), s.OperationStore(),
		mock, bus, disp,
	)
	sessSvc := service.NewSessionService(
		s.SessionStore(), s.SandboxStore(), mock, bus,
	)
	sbxSvc.SetSessionService(sessSvc)
	opsSvc := service.NewOperationService(s.OperationStore(), disp)

	srv := api.NewServer(api.ServerConfig{
		Projects:   projSvc,
		Sandboxes:  sbxSvc,
		Snapshots:  snapSvc,
		Images:     imgSvc,
		Sessions:   sessSvc,
		Operations: opsSvc,
		Provider:   mock,
		Events:     bus,
		Ports:      s.PortBindingStore(),
		AuthToken:  "", // no auth for tests
	})

	return &testEnv{
		handler:    srv.Handler(),
		store:      s,
		mock:       mock,
		events:     bus,
		dispatcher: disp,
		projects:   projSvc,
		sandboxes:  sbxSvc,
		snapshots:  snapSvc,
		images:     imgSvc,
		sessions:   sessSvc,
		operations: opsSvc,
	}
}

func doRequest(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func parseJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
}

func newRequestWithAuth(t *testing.T, method, path string, body any, authHeader string) *http.Request {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reqBody = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func serveHTTP(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
