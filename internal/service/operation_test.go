package service_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func TestOperationServiceGetAndList(t *testing.T) {
	env := newServiceEnv(t)
	opSvc := service.NewOperationService(env.store.OperationStore(), env.dispatcher)

	createOp, _ := env.sandbox.Create(t.Context(), env.projectID, "sbx", "img", service.CreateSandboxOpts{})
	env.dispatcher.WaitIdle()

	got, err := opSvc.Get(t.Context(), createOp.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.OpSucceeded {
		t.Errorf("expected succeeded, got %s", got.State)
	}

	list, err := opSvc.List(t.Context(), domain.OperationFilter{SandboxID: &createOp.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Error("expected at least 1 operation")
	}
}
