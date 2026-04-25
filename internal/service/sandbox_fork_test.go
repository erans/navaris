package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestFork_HandleFork_JSONRoundtrippedMetadata(t *testing.T) {
	env := newServiceEnv(t)
	parent := mustCreateSandbox(t, env)

	// Pre-create the children rows that handleFork will look up.
	now := time.Now().UTC()
	childID1 := uuid.NewString()
	childID2 := uuid.NewString()
	for _, id := range []string{childID1, childID2} {
		child := &domain.Sandbox{
			SandboxID: id,
			ProjectID: parent.ProjectID,
			Name:      "child-" + id[:6],
			State:     domain.SandboxPending,
			Backend:   parent.Backend,
			CreatedAt: now,
			UpdatedAt: now,
			Metadata:  map[string]any{"fork_parent_id": parent.SandboxID},
		}
		if err := env.store.SandboxStore().Create(context.Background(), child); err != nil {
			t.Fatalf("create child: %v", err)
		}
	}

	// Build an op with metadata as it would look after JSON round-trip:
	// "children" is []any with string elements, "count" is float64.
	rawMeta := map[string]any{
		"parent_id": parent.SandboxID,
		"children":  []any{childID1, childID2},
		"count":     float64(2),
	}
	// Sanity-check by JSON-encoding and decoding once — if Go semantics
	// change in the future, the test catches that too.
	enc, err := json.Marshal(rawMeta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(enc, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "sandbox",
		ResourceID:   parent.SandboxID,
		SandboxID:    parent.SandboxID,
		Type:         "fork_sandbox",
		State:        domain.OpPending,
		StartedAt:    now,
		Metadata:     decoded,
	}

	// Drive the worker directly; do not enqueue (the dispatcher path is
	// already covered by the in-memory test).
	if err := env.sandbox.HandleFork(context.Background(), op); err != nil {
		t.Fatalf("handleFork: %v", err)
	}

	for _, id := range []string{childID1, childID2} {
		got, err := env.sandbox.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get child %s: %v", id, err)
		}
		if got.State != domain.SandboxStarting {
			t.Errorf("child %s state = %s, want starting (JSON-decoded path)", id, got.State)
		}
		if got.BackendRef == "" {
			t.Errorf("child %s has empty BackendRef after handleFork", id)
		}
	}
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

func TestFork_RejectsNonRunningParent(t *testing.T) {
	env := newServiceEnv(t)
	// Create a sandbox row directly in pending state, bypassing the dispatcher.
	now := time.Now().UTC()
	pendingParent := &domain.Sandbox{
		SandboxID: uuid.NewString(),
		ProjectID: env.projectID,
		Name:      "pending-parent",
		State:     domain.SandboxPending,
		Backend:   "mock",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := env.store.SandboxStore().Create(context.Background(), pendingParent); err != nil {
		t.Fatalf("create pending parent: %v", err)
	}

	_, err := env.sandbox.Fork(context.Background(), pendingParent.SandboxID, 1)
	if err == nil {
		t.Fatal("expected error when parent is not running")
	}
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Errorf("expected ErrInvalidState, got %v", err)
	}
}
