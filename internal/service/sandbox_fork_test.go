package service_test

import (
	"context"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

// mustCreateSandbox creates a running sandbox in the test environment and
// returns the sandbox record. It fails the test if any step errors.
func mustCreateSandbox(t *testing.T, env *serviceEnv) *domain.Sandbox {
	t.Helper()
	op, err := env.sandbox.Create(t.Context(), env.projectID, "parent-sbx", "img", service.CreateSandboxOpts{})
	if err != nil {
		t.Fatalf("mustCreateSandbox: Create: %v", err)
	}
	env.dispatcher.WaitIdle()
	sbx, err := env.sandbox.Get(t.Context(), op.ResourceID)
	if err != nil {
		t.Fatalf("mustCreateSandbox: Get: %v", err)
	}
	return sbx
}

func TestFork_RejectsCountLessThan1(t *testing.T) {
	env := newServiceEnv(t)
	if _, err := env.sandbox.Fork(context.Background(), "any", 0); err == nil {
		t.Fatal("expected error for count=0")
	}
}

func TestFork_RejectsMissingParent(t *testing.T) {
	env := newServiceEnv(t)
	if _, err := env.sandbox.Fork(context.Background(), "no-such-sbx", 1); err == nil {
		t.Fatal("expected error for missing parent")
	}
}

func TestFork_CreatesNPendingSandboxesAndOperation(t *testing.T) {
	env := newServiceEnv(t)
	parent := mustCreateSandbox(t, env)

	op, err := env.sandbox.Fork(t.Context(), parent.SandboxID, 3)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if op == nil || op.Type != "fork_sandbox" {
		t.Errorf("unexpected operation: %+v", op)
	}

	all, err := env.sandbox.List(t.Context(), domain.SandboxFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	pending := 0
	for _, sb := range all {
		fp, _ := sb.Metadata["fork_parent_id"].(string)
		if sb.State == domain.SandboxPending && fp == parent.SandboxID {
			pending++
		}
	}
	if pending != 3 {
		t.Errorf("pending children = %d, want 3", pending)
	}
}

func TestFork_WorkerBindsChildrenToRefs(t *testing.T) {
	env := newServiceEnv(t)
	parent := mustCreateSandbox(t, env)

	op, err := env.sandbox.Fork(t.Context(), parent.SandboxID, 2)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if op == nil {
		t.Fatal("expected non-nil operation")
	}

	env.dispatcher.WaitIdle()

	all, err := env.sandbox.List(t.Context(), domain.SandboxFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	starting := 0
	for _, sb := range all {
		fp, _ := sb.Metadata["fork_parent_id"].(string)
		if fp == parent.SandboxID {
			if sb.State != domain.SandboxStarting {
				t.Errorf("child %s: expected starting, got %s", sb.SandboxID, sb.State)
			}
			if sb.BackendRef == "" {
				t.Errorf("child %s: expected non-empty BackendRef after fork", sb.SandboxID)
			}
			starting++
		}
	}
	if starting != 2 {
		t.Errorf("starting children = %d, want 2", starting)
	}
}
