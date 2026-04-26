package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

func (e *serviceEnv) seedSandbox(t *testing.T, name string, state domain.SandboxState, backend string) *domain.Sandbox {
	t.Helper()
	cpu, mem := 1, 256
	sbx := &domain.Sandbox{
		SandboxID:     "sbx-" + uuid.NewString()[:8],
		ProjectID:     e.projectID,
		Name:          name,
		State:         state,
		Backend:       backend,
		BackendRef:    "ref-" + uuid.NewString()[:8],
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
		NetworkMode:   domain.NetworkIsolated,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := e.store.SandboxStore().Create(t.Context(), sbx); err != nil {
		t.Fatal(err)
	}
	return sbx
}

func TestUpdateResources_StoppedSandbox(t *testing.T) {
	env := newServiceEnv(t)
	sbx := env.seedSandbox(t, "sbx-stopped", domain.SandboxStopped, "mock")

	calls := 0
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, _ domain.UpdateResourcesRequest) error {
		calls++
		return nil
	}

	ch, cancel, err := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventSandboxResourcesUpdated},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	cpu := 4
	mem := 1024
	res, err := env.sandbox.UpdateResources(t.Context(), service.UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      &cpu,
		MemoryLimitMB: &mem,
	})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}
	if res.AppliedLive {
		t.Fatalf("AppliedLive=true on stopped sandbox; want false")
	}
	if res.Sandbox.CPULimit == nil || *res.Sandbox.CPULimit != 4 {
		t.Fatalf("CPULimit = %v, want 4", res.Sandbox.CPULimit)
	}
	if res.Sandbox.MemoryLimitMB == nil || *res.Sandbox.MemoryLimitMB != 1024 {
		t.Fatalf("MemoryLimitMB = %v, want 1024", res.Sandbox.MemoryLimitMB)
	}

	if calls != 0 {
		t.Fatalf("provider.UpdateResources called %d times; want 0", calls)
	}

	select {
	case ev := <-ch:
		if ev.Type != domain.EventSandboxResourcesUpdated {
			t.Errorf("got event type %q", ev.Type)
		}
		if ev.Data["sandbox_id"] != sbx.SandboxID {
			t.Errorf("event sandbox_id = %v, want %s", ev.Data["sandbox_id"], sbx.SandboxID)
		}
	case <-time.After(time.Second):
		t.Fatal("event not received")
	}
}
