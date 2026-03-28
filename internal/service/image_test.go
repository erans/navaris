package service_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestImageServiceRegister(t *testing.T) {
	env := newServiceEnv(t)
	imgSvc := service.NewImageService(
		env.store.ImageStore(), env.store.SnapshotStore(),
		env.store.OperationStore(), env.mock, env.events, env.dispatcher,
	)

	img, err := imgSvc.Register(t.Context(), "ubuntu", "24.04", "mock", "ref-1", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if img.State != domain.ImageReady {
		t.Errorf("expected ready, got %s", img.State)
	}
}

func TestImageServicePromoteSnapshot(t *testing.T) {
	env := newServiceEnv(t)
	snapSvc := service.NewSnapshotService(
		env.store.SnapshotStore(), env.store.SandboxStore(),
		env.store.OperationStore(), env.mock, env.events, env.dispatcher,
	)
	imgSvc := service.NewImageService(
		env.store.ImageStore(), env.store.SnapshotStore(),
		env.store.OperationStore(), env.mock, env.events, env.dispatcher,
	)

	// Create sandbox, stop it, create snapshot
	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()
	env.sandbox.Stop(t.Context(), createOp.ResourceID, false)
	env.dispatcher.WaitIdle()
	snapOp, _ := snapSvc.Create(t.Context(), createOp.ResourceID, "snap1", domain.ConsistencyStopped)
	env.dispatcher.WaitIdle()

	// Promote
	promoteOp, err := imgSvc.PromoteSnapshot(t.Context(), snapOp.ResourceID, "custom-image", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	env.dispatcher.WaitIdle()

	img, err := imgSvc.Get(t.Context(), promoteOp.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	if img.State != domain.ImageReady {
		t.Errorf("expected ready, got %s", img.State)
	}
}
