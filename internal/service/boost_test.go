package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

type boostEnv struct {
	*serviceEnv
	boost *service.BoostService
}

func newBoostEnv(t *testing.T) *boostEnv {
	t.Helper()
	env := newServiceEnv(t)
	bs := service.NewBoostService(
		env.store.BoostStore(),
		env.store.SandboxStore(),
		env.sandbox,
		env.events,
		service.RealClock{},
		time.Hour,
	)
	return &boostEnv{serviceEnv: env, boost: bs}
}

func TestBoostStart_HappyPath(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-boost", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	ch, cancel, err := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	var calls []domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		calls = append(calls, req)
		return nil
	}

	cpu, mem := 8, 4096
	b, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID:       sbx.SandboxID,
		CPULimit:        &cpu,
		MemoryLimitMB:   &mem,
		DurationSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if b.State != domain.BoostActive {
		t.Errorf("state = %s", b.State)
	}
	if b.BoostedCPULimit == nil || *b.BoostedCPULimit != 8 {
		t.Errorf("BoostedCPU = %+v", b.BoostedCPULimit)
	}
	if b.OriginalCPULimit == nil || *b.OriginalCPULimit != origCPU {
		t.Errorf("OriginalCPU = %+v; want %d", b.OriginalCPULimit, origCPU)
	}
	if !b.ExpiresAt.After(b.StartedAt) {
		t.Errorf("ExpiresAt %v not after StartedAt %v", b.ExpiresAt, b.StartedAt)
	}

	if len(calls) != 1 {
		t.Fatalf("provider.UpdateResources calls = %d; want 1", len(calls))
	}

	// Persisted sandbox row must NOT have been mutated (ApplyLiveOnly=true).
	got, _ := env.store.SandboxStore().Get(t.Context(), sbx.SandboxID)
	if got.CPULimit == nil || *got.CPULimit != origCPU {
		t.Fatalf("persisted CPULimit = %+v; want %d (unchanged)", got.CPULimit, origCPU)
	}

	// Boost row exists in store.
	dbB, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("BoostStore.Get: %v", err)
	}
	if dbB.BoostID != b.BoostID {
		t.Errorf("BoostStore returned wrong row: %s vs %s", dbB.BoostID, b.BoostID)
	}

	select {
	case ev := <-ch:
		if ev.Type != domain.EventBoostStarted {
			t.Errorf("event type = %s", ev.Type)
		}
		if ev.Data["sandbox_id"] != sbx.SandboxID {
			t.Errorf("event sandbox_id = %v", ev.Data["sandbox_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}

}

func TestBoostStart_StoppedSandbox_409(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-stopped", domain.SandboxStopped, "mock")
	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
}

func TestBoostStart_BothFieldsNil_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_DurationZero_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 0,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_DurationOverMax_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	cpu := 4
	// max from newBoostEnv = 1h = 3600s
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 3601,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_BoundsViolation_400(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "firecracker")
	cpu := 99 // FC max is 32
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestBoostStart_ProviderError_RollsBack(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "firecracker")
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		return &domain.ProviderResizeError{Reason: domain.ResizeReasonExceedsCeiling, Detail: "test"}
	}

	cpu := 4
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("err = %v, want *ProviderResizeError", err)
	}

	// Boost row must NOT exist (rolled back).
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not rolled back; got %v", err)
	}
}

func TestBoostStart_ReplacesExisting(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu1 := 4
	first, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu1, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	cpu2 := 8
	second, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu2, DurationSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.BoostID == second.BoostID {
		t.Fatalf("expected new boost id; got same %s", first.BoostID)
	}
	got, _ := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if got.BoostID != second.BoostID {
		t.Fatalf("store has %s, want %s", got.BoostID, second.BoostID)
	}
}
