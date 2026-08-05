package service_test

import (
	"context"
	"errors"
	"sync"
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

// fakeClock advances time on demand and runs scheduled timers synchronously
// when their deadline passes.
type fakeClock struct {
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { t.stopped = true; return true }

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }
func (c *fakeClock) Now() time.Time       { return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, fn func()) service.Timer {
	t := &fakeTimer{at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}

// fire advances the clock by dur and synchronously invokes any timer whose
// deadline has elapsed (in scheduled order). Timers scheduled by callbacks
// during fire accumulate for the next fire call.
func (c *fakeClock) fire(dur time.Duration) {
	c.now = c.now.Add(dur)
	pending := c.timers
	c.timers = nil
	for _, t := range pending {
		if t.stopped || t.at.After(c.now) {
			c.timers = append(c.timers, t)
			continue
		}
		t.fn()
	}
}

func newBoostEnvWithClock(t *testing.T, clk service.Clock) *boostEnv {
	t.Helper()
	env := newServiceEnv(t)
	bs := service.NewBoostService(
		env.store.BoostStore(), env.store.SandboxStore(), env.sandbox,
		env.events, clk, time.Hour,
	)
	return &boostEnv{serviceEnv: env, boost: bs}
}

type faultBoostStore struct {
	domain.BoostStore
	mu                                   sync.Mutex
	upsertErr, deleteErr, updateStateErr error
	getByIDErr                           error
}

func (s *faultBoostStore) take(errp *error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := *errp
	if err != nil {
		*errp = nil
	}
	return err
}

func (s *faultBoostStore) Upsert(ctx context.Context, b *domain.Boost) error {
	if err := s.take(&s.upsertErr); err != nil {
		return err
	}
	return s.BoostStore.Upsert(ctx, b)
}

func (s *faultBoostStore) GetByID(ctx context.Context, boostID string) (*domain.Boost, error) {
	if err := s.take(&s.getByIDErr); err != nil {
		return nil, err
	}
	return s.BoostStore.GetByID(ctx, boostID)
}

func (s *faultBoostStore) UpdateState(ctx context.Context, boostID string, state domain.BoostState, attempts int, lastErr string) error {
	if err := s.take(&s.updateStateErr); err != nil {
		return err
	}
	return s.BoostStore.UpdateState(ctx, boostID, state, attempts, lastErr)
}

func (s *faultBoostStore) Delete(ctx context.Context, boostID string) error {
	if err := s.take(&s.deleteErr); err != nil {
		return err
	}
	return s.BoostStore.Delete(ctx, boostID)
}

type faultSandboxStore struct {
	domain.SandboxStore
	mu     sync.Mutex
	getErr error
}

func (s *faultSandboxStore) takeGet() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.getErr
	if err != nil {
		s.getErr = nil
	}
	return err
}

func (s *faultSandboxStore) Get(ctx context.Context, sandboxID string) (*domain.Sandbox, error) {
	if err := s.takeGet(); err != nil {
		return nil, err
	}
	return s.SandboxStore.Get(ctx, sandboxID)
}

func newBoostEnvWithFaults(t *testing.T, clk service.Clock) (*boostEnv, *faultBoostStore, *faultSandboxStore) {
	t.Helper()
	env := newServiceEnv(t)
	boosts := &faultBoostStore{BoostStore: env.store.BoostStore()}
	sandboxes := &faultSandboxStore{SandboxStore: env.store.SandboxStore()}
	bs := service.NewBoostService(boosts, sandboxes, env.sandbox, env.events, clk, time.Hour)
	env.sandbox.SetBoostService(bs)
	return &boostEnv{serviceEnv: env, boost: bs}, boosts, sandboxes
}

func TestBoostStart_ReplacementApplyFailureRestoresPrior(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, _, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-replace-apply", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	cpuA := 4
	boostA, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("apply replacement failed")
	failNextApply := true
	revertedPrior := false
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		if failNextApply {
			failNextApply = false
			return applyErr
		}
		if req.CPULimit != nil && *req.CPULimit == origCPU {
			revertedPrior = true
		}
		return nil
	}

	cpuB := 8
	_, err = env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
	})
	if !errors.Is(err, applyErr) {
		t.Fatalf("replacement Start error = %v; want apply error", err)
	}
	got, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("prior boost row not restored: %v", err)
	}
	if got.BoostID != boostA.BoostID {
		t.Fatalf("boost row = %s; want prior %s", got.BoostID, boostA.BoostID)
	}

	clk.fire(31 * time.Second)
	if !revertedPrior {
		t.Fatal("restored prior boost did not expire and revert")
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("prior boost row after expiry = %v; want ErrNotFound", err)
	}
}

func TestBoostStart_ReplacementUpsertFailureKeepsPrior(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, boosts, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-replace-upsert", domain.SandboxRunning, "mock")

	cpuA := 4
	boostA, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	upsertErr := errors.New("replacement upsert failed")
	boosts.upsertErr = upsertErr
	revertCalls := 0
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		revertCalls++
		return nil
	}

	cpuB := 8
	_, err = env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
	})
	if !errors.Is(err, upsertErr) {
		t.Fatalf("replacement Start error = %v; want upsert error", err)
	}
	got, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("prior boost row missing after upsert failure: %v", err)
	}
	if got.BoostID != boostA.BoostID {
		t.Fatalf("boost row = %s; want prior %s", got.BoostID, boostA.BoostID)
	}

	clk.fire(31 * time.Second)
	if revertCalls != 1 {
		t.Fatalf("prior expiry revert calls = %d; want 1", revertCalls)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("prior boost row after expiry = %v; want ErrNotFound", err)
	}
}

func TestBoostExpire_TransientSandboxLookupRearamsRetry(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, _, sandboxes := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-expire-lookup", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}

	lookupErr := errors.New("sandbox lookup unavailable")
	sandboxes.getErr = lookupErr
	revertCalls := 0
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		revertCalls++
		return nil
	}

	clk.fire(11 * time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("boost row not preserved after transient lookup: %v", err)
	}
	clk.fire(time.Second)
	if revertCalls != 1 {
		t.Fatalf("retry revert calls = %d; want 1", revertCalls)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row after retry = %v; want ErrNotFound", err)
	}
}

func TestBoostExpire_DeleteFailureRearamsCleanup(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, boosts, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-expire-delete", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}

	boosts.deleteErr = errors.New("delete failed")
	clk.fire(11 * time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("boost row not preserved after delete failure: %v", err)
	}
	clk.fire(time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row after cleanup retry = %v; want ErrNotFound", err)
	}
}

func TestBoostExpire_UpdateStateFailureRearamsRetry(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, boosts, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-expire-state", domain.SandboxRunning, "mock")

	cpu := 8
	boost, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.BoostStore().UpdateState(t.Context(), boost.BoostID, domain.BoostActive, 5, "prior failures"); err != nil {
		t.Fatal(err)
	}

	applyErr := errors.New("provider revert failed")
	stateErr := errors.New("state update failed")
	boosts.updateStateErr = stateErr
	revertCalls := 0
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		revertCalls++
		return applyErr
	}

	clk.fire(11 * time.Second)
	got, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("boost row not preserved after state update failure: %v", err)
	}
	if got.State != domain.BoostActive {
		t.Fatalf("state after failed UpdateState = %s; want active", got.State)
	}
	clk.fire(time.Second)
	if revertCalls != 2 {
		t.Fatalf("retry revert calls = %d; want 2", revertCalls)
	}
	got, err = env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("boost row after retry missing: %v", err)
	}
	if got.State != domain.BoostRevertFailed {
		t.Fatalf("state after retry = %s; want revert_failed", got.State)
	}
}

func TestBoostCancel_TransientLookupPreservesExpiry(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, _, sandboxes := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-cancel-lookup", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}

	lookupErr := errors.New("sandbox lookup unavailable")
	sandboxes.getErr = lookupErr
	if err := env.boost.Cancel(t.Context(), sbx.SandboxID); !errors.Is(err, lookupErr) {
		t.Fatalf("Cancel error = %v; want lookup error", err)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("boost row not preserved after cancel lookup failure: %v", err)
	}
	clk.fire(11 * time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row after restored expiry = %v; want ErrNotFound", err)
	}
}

func TestBoostCancel_DeleteFailureReturnsAndRearams(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, boosts, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-cancel-delete", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}

	deleteErr := errors.New("delete failed")
	boosts.deleteErr = deleteErr
	if err := env.boost.Cancel(t.Context(), sbx.SandboxID); !errors.Is(err, deleteErr) {
		t.Fatalf("Cancel error = %v; want delete error", err)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("boost row not preserved after cancel delete failure: %v", err)
	}
	clk.fire(time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row after cancel cleanup retry = %v; want ErrNotFound", err)
	}
}

func TestBoostLifecycle_DeleteFailureLeavesTimerArmed(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env, boosts, _ := newBoostEnvWithFaults(t, clk)
	sbx := env.seedSandbox(t, "sbx-lifecycle-delete", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 10,
	}); err != nil {
		t.Fatal(err)
	}

	boosts.deleteErr = errors.New("delete failed")
	if _, err := env.sandbox.Stop(t.Context(), sbx.SandboxID, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	env.dispatcher.WaitIdle()
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("boost row not preserved after lifecycle delete failure: %v", err)
	}
	clk.fire(11 * time.Second)
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row after lifecycle timer fired = %v; want ErrNotFound", err)
	}
}

func TestBoostExpire_RevertsToCurrentPersisted(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	cpu := 8
	_, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, cancel, _ := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostExpired},
	})
	defer cancel()

	var lastReq domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		lastReq = req
		return nil
	}

	clk.fire(61 * time.Second)

	if lastReq.CPULimit == nil || *lastReq.CPULimit != origCPU {
		t.Fatalf("revert called with CPULimit=%+v; want %d", lastReq.CPULimit, origCPU)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not deleted on expire; got %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Data["cause"] != "expired" {
			t.Errorf("event cause = %v", ev.Data["cause"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostExpired not received")
	}
}

func TestBoostExpire_RetriesOnFailure_ThenRevertFailed(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	// Boost apply already succeeded above (Start used the default nil-returning mock).
	// Arm a failure for every revert call from expire onwards.
	calls := 0
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		calls++
		return errors.New("provider boom")
	}

	clk.fire(61 * time.Second) // attempt 1 fails -> 1s retry scheduled
	clk.fire(2 * time.Second)  // attempt 2 fails -> 5s retry
	clk.fire(6 * time.Second)  // 3 -> 30s
	clk.fire(31 * time.Second) // 4 -> 2m
	clk.fire(2 * time.Minute)  // 5 -> 10m
	clk.fire(11 * time.Minute) // 6 -> exhausted

	got, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatalf("expected boost row to remain in revert_failed; got %v", err)
	}
	if got.State != domain.BoostRevertFailed {
		t.Fatalf("state = %s, want revert_failed", got.State)
	}
	if got.RevertAttempts < 6 {
		t.Fatalf("revert_attempts = %d, want >= 6", got.RevertAttempts)
	}
}

func TestBoostCancel_Reverts(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	origCPU := *sbx.CPULimit

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	var lastReq domain.UpdateResourcesRequest
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		lastReq = req
		return nil
	}

	if err := env.boost.Cancel(t.Context(), sbx.SandboxID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if lastReq.CPULimit == nil || *lastReq.CPULimit != origCPU {
		t.Fatalf("revert called with CPULimit=%+v; want %d", lastReq.CPULimit, origCPU)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not deleted after Cancel; got %v", err)
	}
}

func TestBoostCancel_NoActiveBoost_NotFound(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")
	err := env.boost.Cancel(t.Context(), sbx.SandboxID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSandboxStop_CancelsBoost(t *testing.T) {
	env := newBoostEnv(t)
	env.sandbox.SetBoostService(env.boost)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	// Stop the sandbox. The boost row must be gone afterwards.
	if _, err := env.sandbox.Stop(t.Context(), sbx.SandboxID, false); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	env.dispatcher.WaitIdle()

	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost row not removed by Stop hook; got %v", err)
	}
}

func TestBoostRecover_RescheduleInWindow(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	// Seed a boost row directly (simulates a row left over from a daemon restart).
	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now,
		ExpiresAt: now.Add(60 * time.Second),
		State:     domain.BoostActive,
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Advancing past expiry must trigger the revert.
	called := false
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		called = true
		return nil
	}
	clk.fire(61 * time.Second)
	if !called {
		t.Fatal("recovered boost did not expire on time")
	}
}

func TestBoostRecover_AlreadyExpired_RevertsImmediately(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now.Add(-10 * time.Minute),
		ExpiresAt: now.Add(-5 * time.Minute), // already in the past
		State:     domain.BoostActive,
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	called := make(chan struct{}, 1)
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("expired-while-down boost did not revert immediately")
	}
}

func TestBoostRecover_RevertFailedLeftAlone(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	cpu := 8
	now := clk.Now().UTC()
	row := &domain.Boost{
		BoostID: "bst-x", SandboxID: sbx.SandboxID,
		BoostedCPULimit: &cpu, StartedAt: now,
		ExpiresAt:      now.Add(60 * time.Second),
		State:          domain.BoostRevertFailed,
		RevertAttempts: 5,
		LastError:      "stuck",
	}
	if err := env.store.BoostStore().Upsert(t.Context(), row); err != nil {
		t.Fatal(err)
	}

	if err := env.boost.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Advance past expiry — no revert should fire.
	called := false
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef, domain.UpdateResourcesRequest) error {
		called = true
		return nil
	}
	clk.fire(61 * time.Second)
	if called {
		t.Fatal("revert_failed boost should not auto-revert on Recover")
	}
}

func TestBoostStart_EmitsSource(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	ch, cancel, err := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	cpu := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
		Source: "in_sandbox",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Data["source"] != "in_sandbox" {
			t.Errorf("source = %v, want in_sandbox", ev.Data["source"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}
}

func TestBoostStart_DefaultSourceExternal(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx", domain.SandboxRunning, "mock")

	ch, cancel, err := env.events.Subscribe(t.Context(), domain.EventFilter{
		Types: []domain.EventType{domain.EventBoostStarted},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	cpu := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
		// no Source set
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Data["source"] != "external" {
			t.Errorf("source = %v, want external (default)", ev.Data["source"])
		}
	case <-time.After(time.Second):
		t.Fatal("EventBoostStarted not received")
	}
}
