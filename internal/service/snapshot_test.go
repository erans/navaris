package service_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestSnapshotServiceCreate(t *testing.T) {
	env := newServiceEnv(t)
	// Create sandbox, wait for running, then stop it
	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()
	env.sandbox.Stop(t.Context(), createOp.ResourceID, false)
	env.dispatcher.WaitIdle()

	snapSvc := service.NewSnapshotService(
		env.store.SnapshotStore(), env.store.SandboxStore(),
		env.store.OperationStore(), env.mock, env.events, env.dispatcher,
	)

	op, err := snapSvc.Create(t.Context(), createOp.ResourceID, "snap1", domain.ConsistencyStopped)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	snap, err := snapSvc.Get(t.Context(), op.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != domain.SnapshotReady {
		t.Errorf("expected ready, got %s", snap.State)
	}
}

func TestSnapshotServiceLiveMode(t *testing.T) {
	env := newServiceEnv(t)
	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	snapSvc := service.NewSnapshotService(
		env.store.SnapshotStore(), env.store.SandboxStore(),
		env.store.OperationStore(), env.mock, env.events, env.dispatcher,
	)

	// Live mode — sandbox can be running
	op, err := snapSvc.Create(t.Context(), createOp.ResourceID, "live-snap", domain.ConsistencyLive)
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	snap, _ := snapSvc.Get(t.Context(), op.ResourceID)
	if snap.State != domain.SnapshotReady {
		t.Errorf("expected ready, got %s", snap.State)
	}
}
