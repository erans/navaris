package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/provider"
	"github.com/navaris/navaris/internal/service"
	"github.com/navaris/navaris/internal/store/sqlite"
	"github.com/navaris/navaris/internal/worker"
)

type serviceEnv struct {
	store      *sqlite.Store
	mock       *provider.MockProvider
	events     *eventbus.MemoryBus
	dispatcher *worker.Dispatcher
	project    *service.ProjectService
	sandbox    *service.SandboxService
	projectID  string
}

func newServiceEnv(t *testing.T) *serviceEnv {
	t.Helper()
	s, err := sqlite.Open(":memory:")
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

	proj, err := projSvc.Create(t.Context(), "test-project-"+uuid.NewString()[:8], nil)
	if err != nil {
		t.Fatal(err)
	}

	return &serviceEnv{
		store: s, mock: mock, events: bus, dispatcher: disp,
		project: projSvc, sandbox: sbxSvc, projectID: proj.ProjectID,
	}
}

func TestSandboxServiceCreate(t *testing.T) {
	env := newServiceEnv(t)
	op, err := env.sandbox.Create(t.Context(), env.projectID, "my-sandbox", "image-1", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if op.State != domain.OpPending {
		t.Error("expected pending")
	}

	env.dispatcher.WaitIdle()

	sbx, err := env.sandbox.Get(t.Context(), op.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	if sbx.State != domain.SandboxRunning {
		t.Errorf("expected running, got %s", sbx.State)
	}
}

func TestSandboxServiceStartStop(t *testing.T) {
	env := newServiceEnv(t)
	op, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	// Stop
	stopOp, err := env.sandbox.Stop(t.Context(), op.ResourceID, false)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	sbx, _ := env.sandbox.Get(t.Context(), stopOp.ResourceID)
	if sbx.State != domain.SandboxStopped {
		t.Errorf("expected stopped, got %s", sbx.State)
	}

	// Start
	startOp, err := env.sandbox.Start(t.Context(), op.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	sbx, _ = env.sandbox.Get(t.Context(), startOp.ResourceID)
	if sbx.State != domain.SandboxRunning {
		t.Errorf("expected running, got %s", sbx.State)
	}
}

func TestSandboxServiceDestroy(t *testing.T) {
	env := newServiceEnv(t)
	op, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	destroyOp, err := env.sandbox.Destroy(t.Context(), op.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	sbx, _ := env.sandbox.Get(t.Context(), destroyOp.ResourceID)
	if sbx.State != domain.SandboxDestroyed {
		t.Errorf("expected destroyed, got %s", sbx.State)
	}
}

func TestSandboxServiceInvalidStateTransition(t *testing.T) {
	env := newServiceEnv(t)
	// Create a sandbox and wait for it to be running
	op, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	// Try to start a running sandbox — should fail
	_, err := env.sandbox.Start(t.Context(), op.ResourceID)
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

func TestSandboxServiceCreateWithOptions(t *testing.T) {
	env := newServiceEnv(t)
	cpu := 4
	mem := 2048
	expires := time.Now().UTC().Add(24 * time.Hour)
	op, err := env.sandbox.Create(t.Context(), env.projectID, "opts-sbx", "img", service.CreateSandboxOpts{
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
		ExpiresAt:     &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	sbx, _ := env.sandbox.Get(t.Context(), op.ResourceID)
	if *sbx.CPULimit != 4 {
		t.Errorf("wrong cpu: %d", *sbx.CPULimit)
	}
	if *sbx.MemoryLimitMB != 2048 {
		t.Errorf("wrong memory: %d", *sbx.MemoryLimitMB)
	}
}
