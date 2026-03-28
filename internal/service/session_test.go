package service_test

import (
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestSessionServiceCreate(t *testing.T) {
	env := newServiceEnv(t)
	sessSvc := service.NewSessionService(
		env.store.SessionStore(), env.store.SandboxStore(), env.mock, env.events,
	)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	sess, err := sessSvc.Create(t.Context(), createOp.ResourceID, domain.SessionBackingDirect, "/bin/bash")
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != domain.SessionActive {
		t.Errorf("expected active, got %s", sess.State)
	}
}

func TestSessionServiceCreateOnStoppedSandbox(t *testing.T) {
	env := newServiceEnv(t)
	sessSvc := service.NewSessionService(
		env.store.SessionStore(), env.store.SandboxStore(), env.mock, env.events,
	)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()
	env.sandbox.Stop(t.Context(), createOp.ResourceID, false)
	env.dispatcher.WaitIdle()

	_, err := sessSvc.Create(t.Context(), createOp.ResourceID, domain.SessionBackingDirect, "/bin/bash")
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}

func TestSessionServiceDestroy(t *testing.T) {
	env := newServiceEnv(t)
	sessSvc := service.NewSessionService(
		env.store.SessionStore(), env.store.SandboxStore(), env.mock, env.events,
	)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()
	sess, _ := sessSvc.Create(t.Context(), createOp.ResourceID, "", "")
	env.dispatcher.WaitIdle()

	if err := sessSvc.Destroy(t.Context(), sess.SessionID); err != nil {
		t.Fatal(err)
	}
	got, _ := sessSvc.Get(t.Context(), sess.SessionID)
	if got.State != domain.SessionDestroyed {
		t.Errorf("expected destroyed, got %s", got.State)
	}
}
