package api_test

import (
	"net/http"
	"testing"

	"github.com/navaris/navaris/internal/api"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

func newTestEnvWithAuth(t *testing.T, token string) *testEnv {
	t.Helper()
	dsn := "file:authdb?mode=memory&cache=shared"
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
		s.SandboxStore(), s.OperationStore(), s.PortBindingStore(),
		s.SessionStore(), mock, bus, disp,
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
		AuthToken:  token,
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

func TestAuthMiddlewareRejectsNoToken(t *testing.T) {
	env := newTestEnvWithAuth(t, "secret-token")

	rec := doRequest(t, env.handler, "GET", "/v1/health", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddlewareAcceptsBearerToken(t *testing.T) {
	env := newTestEnvWithAuth(t, "secret-token")

	req := newRequestWithAuth(t, "GET", "/v1/health", nil, "Bearer secret-token")
	rec := serveHTTP(env.handler, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddlewareAcceptsQueryToken(t *testing.T) {
	env := newTestEnvWithAuth(t, "secret-token")

	rec := doRequest(t, env.handler, "GET", "/v1/health?token=secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	reqID := rec.Header().Get("X-Request-ID")
	if reqID == "" {
		t.Fatal("expected X-Request-ID header to be set")
	}
}

func TestHealthCheck(t *testing.T) {
	env := newTestEnv(t)

	rec := doRequest(t, env.handler, "GET", "/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	parseJSON(t, rec, &resp)
	if resp["Healthy"] != true {
		t.Fatalf("expected healthy true, got %v", resp["Healthy"])
	}
}
