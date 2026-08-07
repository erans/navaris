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

const boostRaceTimeout = 2 * time.Second

func receiveWithin[T any](t *testing.T, ch <-chan T, label string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(boostRaceTimeout):
		var zero T
		t.Fatalf("timed out waiting for %s", label)
		return zero
	}
}

func waitForSandboxLockRefs(t *testing.T, bs *service.BoostService, sandboxID string, want int) bool {
	t.Helper()
	deadline := time.Now().Add(boostRaceTimeout)
	for time.Now().Before(deadline) {
		if got := bs.SandboxLockRefsForTest(sandboxID); got == want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

type boostStoreHook struct {
	domain.BoostStore
	afterGetByID func(string, *domain.Boost, error)
}

func (h *boostStoreHook) GetByID(ctx context.Context, boostID string) (*domain.Boost, error) {
	b, err := h.BoostStore.GetByID(ctx, boostID)
	if h.afterGetByID != nil {
		h.afterGetByID(boostID, b, err)
	}
	return b, err
}

type sandboxStoreHook struct {
	domain.SandboxStore
	beforeUpdate func(context.Context, *domain.Sandbox)
}

func (h *sandboxStoreHook) Update(ctx context.Context, sbx *domain.Sandbox) error {
	if h.beforeUpdate != nil {
		h.beforeUpdate(ctx, sbx)
	}
	return h.SandboxStore.Update(ctx, sbx)
}

type operationStoreHook struct {
	domain.OperationStore
	beforeCreate func(context.Context, *domain.Operation) error
}

func (h *operationStoreHook) Create(ctx context.Context, op *domain.Operation) error {
	if h.beforeCreate != nil {
		if err := h.beforeCreate(ctx, op); err != nil {
			return err
		}
	}
	return h.OperationStore.Create(ctx, op)
}

func newBoostEnvWithHooks(
	t *testing.T,
	sandboxHook func(context.Context, *domain.Sandbox),
	operationHook func(context.Context, *domain.Operation) error,
) *boostEnv {
	t.Helper()
	env := newServiceEnv(t)

	var sandboxStore domain.SandboxStore = env.store.SandboxStore()
	if sandboxHook != nil {
		sandboxStore = &sandboxStoreHook{
			SandboxStore: sandboxStore,
			beforeUpdate: sandboxHook,
		}
	}
	var operationStore domain.OperationStore = env.store.OperationStore()
	if operationHook != nil {
		operationStore = &operationStoreHook{
			OperationStore: operationStore,
			beforeCreate:   operationHook,
		}
	}

	env.sandbox = service.NewSandboxService(
		sandboxStore, env.store.SnapshotStore(), operationStore, env.store.PortBindingStore(),
		env.store.SessionStore(), env.mock, env.events, env.dispatcher, "mock", false,
	)
	boost := service.NewBoostService(
		env.store.BoostStore(), sandboxStore, env.sandbox, env.events, service.RealClock{}, time.Hour,
	)
	env.sandbox.SetBoostService(boost)
	return &boostEnv{serviceEnv: env, boost: boost}
}

// TestBoostStartVsExpire_SerializeApply verifies the F7 fix: Start(B) and
// expire(A) for the same sandbox must serialize their UpdateResources apply
// calls via the per-sandbox lock. Without the fix, both applies run
// concurrently (no shared lock), so the test observes Start(B)'s apply while
// expire(A) is still mid-apply. With the fix, Start(B) blocks on sbxMu until
// expire(A) releases it, so Start(B)'s apply cannot overlap expire(A)'s.
//
// Mechanism: UpdateResourcesFn signals applyCh (cpu value) then blocks on
// releaseCh, letting the test hold each apply open. The test starts A, fires
// expire(A) (held open), then starts B and asserts B does NOT apply while
// expire(A) is held. It then releases expire(A) and asserts B applies next.
func TestBoostStartVsExpire_SerializeApply(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	env := newBoostEnvWithClock(t, clk)
	sbx := env.seedSandbox(t, "sbx-ser", domain.SandboxRunning, "mock")

	applyCh := make(chan int)        // signals an apply started (cpu value)
	releaseCh := make(chan struct{}) // test gates each apply's return
	var mu sync.Mutex
	var order []int
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef, req domain.UpdateResourcesRequest) error {
		cpu := -1
		if req.CPULimit != nil {
			cpu = *req.CPULimit
		}
		mu.Lock()
		order = append(order, cpu)
		mu.Unlock()
		applyCh <- cpu
		<-releaseCh
		return nil
	}

	cpuA := 2
	doneA := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
		})
		doneA <- err
	}()
	_ = receiveWithin(t, applyCh, "Start(A) apply")
	releaseCh <- struct{}{}
	if err := receiveWithin(t, doneA, "Start(A) completion"); err != nil {
		t.Fatalf("Start(A): %v", err)
	}

	// Launch expire(A) in a goroutine (fire the timer). Its revert apply starts
	// and blocks on releaseCh, holding the per-sandbox lock (with the fix).
	go clk.fire(61 * time.Second)
	_ = receiveWithin(t, applyCh, "expire(A) revert apply")

	// Launch Start(B) in a goroutine. With the fix, Start(B) blocks on the
	// per-sandbox lock (expire holds it), so its apply does NOT start.
	// Without the fix, Start(B) applies concurrently and sends on applyCh.
	done := make(chan error, 1)
	go func() {
		cpuB := 4
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		done <- err
	}()
	if !waitForSandboxLockRefs(t, env.boost, sbx.SandboxID, 2) {
		releaseCh <- struct{}{}
		t.Fatal("Start(B) did not wait behind expire(A)'s sandbox operation")
	}
	select {
	case <-applyCh:
		releaseCh <- struct{}{}
		t.Fatal("Start(B) applied concurrently with expire(A); per-sandbox lock did not serialize (F7 not fixed)")
	default:
		// Good: Start(B) is blocked on the per-sandbox lock.
	}

	// Release expire(A); it finishes and releases the per-sandbox lock.
	// Start(B) then acquires the lock and applies cpu=4.
	releaseCh <- struct{}{}
	if cpu := receiveWithin(t, applyCh, "Start(B) apply after expire release"); cpu != 4 {
		t.Fatalf("after expire release, apply cpu = %d; want 4 (B)", cpu)
	}
	releaseCh <- struct{}{} // let Start(B) finish
	if err := receiveWithin(t, done, "Start(B) completion"); err != nil {
		t.Fatalf("Start(B): %v", err)
	}

	mu.Lock()
	last := order[len(order)-1]
	mu.Unlock()
	if last != 4 {
		t.Fatalf("last apply = %d; want 4 (B's limit)", last)
	}
}

func TestBoostStartVsStart_BookkeepingAndApplyShareOrder(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-start-order", domain.SandboxRunning, "mock")

	type applyCall struct {
		cpu     int
		release chan struct{}
	}
	entered := make(chan applyCall, 2)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef,
		req domain.UpdateResourcesRequest) error {
		call := applyCall{cpu: *req.CPULimit, release: make(chan struct{})}
		entered <- call
		<-call.release
		return nil
	}

	cpuA, cpuB := 4, 8
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
		})
		doneA <- err
	}()
	callA := receiveWithin(t, entered, "Start(A) apply")
	rowA, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		doneB <- err
	}()
	if !waitForSandboxLockRefs(t, env.boost, sbx.SandboxID, 2) {
		close(callA.release)
		t.Fatal("Start(B) did not wait behind Start(A)'s sandbox operation")
	}

	whileA, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if whileA.BoostID != rowA.BoostID {
		close(callA.release)
		t.Fatalf("Start(B) replaced bookkeeping while Start(A) was applying: got %s want %s",
			whileA.BoostID, rowA.BoostID)
	}
	select {
	case callB := <-entered:
		close(callB.release)
		close(callA.release)
		t.Fatal("Start(B) applied while Start(A) held the sandbox operation")
	default:
	}

	close(callA.release)
	if err := receiveWithin(t, doneA, "Start(A) completion"); err != nil {
		t.Fatal(err)
	}
	callB := receiveWithin(t, entered, "Start(B) apply")
	if callB.cpu != cpuB {
		t.Fatalf("second apply cpu=%d; want %d", callB.cpu, cpuB)
	}
	close(callB.release)
	if err := receiveWithin(t, doneB, "Start(B) completion"); err != nil {
		t.Fatal(err)
	}

	final, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BoostedCPULimit == nil || *final.BoostedCPULimit != cpuB {
		t.Fatalf("final boost cpu=%v; want %d", final.BoostedCPULimit, cpuB)
	}
}

func TestBoostStart_DifferentSandboxesApplyConcurrently(t *testing.T) {
	env := newBoostEnv(t)
	a := env.seedSandbox(t, "sbx-a", domain.SandboxRunning, "mock")
	b := env.seedSandbox(t, "sbx-b", domain.SandboxRunning, "mock")

	entered := make(chan chan struct{}, 2)
	env.mock.UpdateResourcesFn = func(context.Context, domain.BackendRef,
		domain.UpdateResourcesRequest) error {
		release := make(chan struct{})
		entered <- release
		<-release
		return nil
	}

	cpuA, cpuB := 4, 8
	done := make(chan error, 2)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: a.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
		})
		done <- err
	}()
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: b.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		done <- err
	}()

	var releases []chan struct{}
	for len(releases) < 2 {
		select {
		case release := <-entered:
			releases = append(releases, release)
		case <-time.After(boostRaceTimeout):
			t.Fatal("different sandboxes did not enter apply concurrently")
		}
	}
	for _, release := range releases {
		close(release)
	}
	for range 2 {
		if err := receiveWithin(t, done, "cross-sandbox Start completion"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBoostStartVsCancel_SerializeBookkeepingAndApply(t *testing.T) {
	env := newBoostEnv(t)
	sbx := env.seedSandbox(t, "sbx-cancel-order", domain.SandboxRunning, "mock")

	cpuA := 4
	boostA, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	type applyCall struct {
		cpu     int
		release chan struct{}
	}
	entered := make(chan applyCall, 2)
	env.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef,
		req domain.UpdateResourcesRequest) error {
		call := applyCall{cpu: *req.CPULimit, release: make(chan struct{})}
		entered <- call
		<-call.release
		return nil
	}

	cancelDone := make(chan error, 1)
	go func() { cancelDone <- env.boost.Cancel(context.Background(), sbx.SandboxID) }()
	cancelCall := receiveWithin(t, entered, "Cancel revert apply")

	cpuB := 8
	startDone := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		startDone <- err
	}()
	if !waitForSandboxLockRefs(t, env.boost, sbx.SandboxID, 2) {
		close(cancelCall.release)
		t.Fatal("Start did not wait behind Cancel's sandbox operation")
	}

	row, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if row.BoostID != boostA.BoostID {
		close(cancelCall.release)
		t.Fatalf("Start replaced boost while Cancel revert was applying: got %s want %s",
			row.BoostID, boostA.BoostID)
	}
	select {
	case startCall := <-entered:
		close(startCall.release)
		close(cancelCall.release)
		t.Fatal("Start applied concurrently with Cancel")
	default:
	}

	close(cancelCall.release)
	if err := receiveWithin(t, cancelDone, "Cancel completion"); err != nil {
		t.Fatal(err)
	}
	startCall := receiveWithin(t, entered, "post-Cancel Start apply")
	if startCall.cpu != cpuB {
		t.Fatalf("post-Cancel Start cpu=%d; want %d", startCall.cpu, cpuB)
	}
	close(startCall.release)
	if err := receiveWithin(t, startDone, "post-Cancel Start completion"); err != nil {
		t.Fatal(err)
	}

	final, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BoostedCPULimit == nil || *final.BoostedCPULimit != cpuB {
		t.Fatalf("final boost cpu=%v; want %d", final.BoostedCPULimit, cpuB)
	}
}

func TestBoostExpire_StaleTimerDoesNotTouchReplacement(t *testing.T) {
	env := newServiceEnv(t)
	expiryReadA := make(chan struct{}, 1)
	resumeExpiry := make(chan struct{})
	var resumeOnce sync.Once
	releaseExpiry := func() { resumeOnce.Do(func() { close(resumeExpiry) }) }
	t.Cleanup(releaseExpiry)

	var staleID string
	var mu sync.Mutex
	getByIDCalls := map[string]int{}
	hookedBoosts := &boostStoreHook{BoostStore: env.store.BoostStore()}
	hookedBoosts.afterGetByID = func(boostID string, b *domain.Boost, err error) {
		mu.Lock()
		currentStaleID := staleID
		if boostID == currentStaleID {
			getByIDCalls[boostID]++
		}
		count := getByIDCalls[boostID]
		mu.Unlock()
		if boostID == currentStaleID && count == 1 && err == nil && b != nil {
			expiryReadA <- struct{}{}
			<-resumeExpiry
		}
	}

	boost := service.NewBoostService(
		hookedBoosts, env.store.SandboxStore(), env.sandbox, env.events, service.RealClock{}, time.Hour,
	)
	envWithBoost := &boostEnv{serviceEnv: env, boost: boost}
	sbx := envWithBoost.seedSandbox(t, "sbx-stale-expiry", domain.SandboxRunning, "mock")

	cpuA := 4
	boostA, err := envWithBoost.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	staleID = boostA.BoostID
	mu.Unlock()

	var callsMu sync.Mutex
	var appliedCPUs []int
	envWithBoost.mock.UpdateResourcesFn = func(_ context.Context, _ domain.BackendRef,
		req domain.UpdateResourcesRequest) error {
		cpu := -1
		if req.CPULimit != nil {
			cpu = *req.CPULimit
		}
		callsMu.Lock()
		appliedCPUs = append(appliedCPUs, cpu)
		callsMu.Unlock()
		return nil
	}

	_ = receiveWithin(t, expiryReadA, "expiry first GetByID(A)")

	cpuB := 8
	boostB, err := envWithBoost.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseExpiry()

	mu.Lock()
	calls := getByIDCalls[staleID]
	mu.Unlock()
	deadline := time.Now().Add(boostRaceTimeout)
	for calls < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		mu.Lock()
		calls = getByIDCalls[staleID]
		mu.Unlock()
	}
	if calls != 2 {
		t.Fatalf("expiry GetByID(%s) calls=%d; want 2", staleID, calls)
	}

	final, err := envWithBoost.store.BoostStore().Get(t.Context(), sbx.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BoostID != boostB.BoostID {
		t.Fatalf("final boost id=%s; want replacement %s", final.BoostID, boostB.BoostID)
	}
	if final.BoostedCPULimit == nil || *final.BoostedCPULimit != cpuB {
		t.Fatalf("final boost cpu=%v; want %d", final.BoostedCPULimit, cpuB)
	}

	callsMu.Lock()
	gotCPUs := append([]int(nil), appliedCPUs...)
	callsMu.Unlock()
	if len(gotCPUs) != 1 || gotCPUs[0] != cpuB {
		t.Fatalf("provider apply CPUs after stale expiry = %v; want [%d]", gotCPUs, cpuB)
	}
}

func TestBoostStartVsSandboxStop_LifecycleExclusionPreventsRecreatedBoost(t *testing.T) {
	updateEntered := make(chan struct{}, 1)
	releaseUpdate := make(chan struct{})
	env := newBoostEnvWithHooks(t, func(_ context.Context, sbx *domain.Sandbox) {
		if sbx.State != domain.SandboxStopping {
			return
		}
		updateEntered <- struct{}{}
		<-releaseUpdate
	}, nil)
	sbx := env.seedSandbox(t, "sbx-stop-race", domain.SandboxRunning, "mock")

	cpuA := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	stopDone := make(chan error, 1)
	go func() {
		_, err := env.sandbox.Stop(context.Background(), sbx.SandboxID, false)
		stopDone <- err
	}()
	_ = receiveWithin(t, updateEntered, "Stop persisted stopping update")

	cpuB := 8
	startDone := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		startDone <- err
	}()
	if !waitForSandboxLockRefs(t, env.boost, sbx.SandboxID, 2) {
		close(releaseUpdate)
		_ = receiveWithin(t, stopDone, "Stop completion after failed readiness")
		_ = receiveWithin(t, startDone, "Start completion after failed readiness")
		t.Fatal("Boost Start did not wait behind Stop lifecycle exclusion")
	}

	close(releaseUpdate)
	if err := receiveWithin(t, stopDone, "Stop completion"); err != nil {
		t.Fatal(err)
	}
	if err := receiveWithin(t, startDone, "Boost Start after Stop exclusion"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("Boost Start error = %v; want ErrInvalidState", err)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost survived Stop lifecycle exclusion; got %v", err)
	}
}

func TestBoostStartVsSandboxDestroy_LifecycleExclusionPreventsRecreatedBoost(t *testing.T) {
	createEntered := make(chan struct{}, 1)
	releaseCreate := make(chan struct{})
	env := newBoostEnvWithHooks(t, nil, func(_ context.Context, op *domain.Operation) error {
		if op.Type != "destroy_sandbox" {
			return nil
		}
		createEntered <- struct{}{}
		<-releaseCreate
		return nil
	})
	sbx := env.seedSandbox(t, "sbx-destroy-race", domain.SandboxRunning, "mock")

	cpuA := 4
	if _, err := env.boost.Start(t.Context(), service.StartBoostOpts{
		SandboxID: sbx.SandboxID, CPULimit: &cpuA, DurationSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	destroyDone := make(chan error, 1)
	go func() {
		_, err := env.sandbox.Destroy(context.Background(), sbx.SandboxID)
		destroyDone <- err
	}()
	_ = receiveWithin(t, createEntered, "Destroy operation create")

	cpuB := 8
	startDone := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		startDone <- err
	}()
	startErr := receiveWithin(t, startDone, "Boost Start during Destroy exclusion")
	close(releaseCreate)
	if err := receiveWithin(t, destroyDone, "Destroy completion"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(startErr, domain.ErrInvalidState) {
		t.Fatalf("Boost Start error = %v; want ErrInvalidState", startErr)
	}
	if _, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("boost survived Destroy lifecycle exclusion; got %v", err)
	}
}

func TestSandboxLifecycle_OperationCreateFailureRetainsBoost(t *testing.T) {
	for _, tt := range []struct {
		name   string
		opType string
		run    func(context.Context, *service.SandboxService, string) (*domain.Operation, error)
	}{
		{
			name:   "stop",
			opType: "stop_sandbox",
			run: func(ctx context.Context, svc *service.SandboxService, sandboxID string) (*domain.Operation, error) {
				return svc.Stop(ctx, sandboxID, false)
			},
		},
		{
			name:   "destroy",
			opType: "destroy_sandbox",
			run: func(ctx context.Context, svc *service.SandboxService, sandboxID string) (*domain.Operation, error) {
				return svc.Destroy(ctx, sandboxID)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			createErr := errors.New("operation create failed")
			env := newBoostEnvWithHooks(t, nil, func(_ context.Context, op *domain.Operation) error {
				if op.Type == tt.opType {
					return createErr
				}
				return nil
			})
			sbx := env.seedSandbox(t, "sbx-op-fail-"+tt.name, domain.SandboxRunning, "mock")

			cpu := 4
			boost, err := env.boost.Start(t.Context(), service.StartBoostOpts{
				SandboxID: sbx.SandboxID, CPULimit: &cpu, DurationSeconds: 60,
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = tt.run(t.Context(), env.sandbox, sbx.SandboxID)
			if !errors.Is(err, createErr) {
				t.Fatalf("lifecycle error = %v; want operation create failure", err)
			}

			gotSandbox, err := env.store.SandboxStore().Get(t.Context(), sbx.SandboxID)
			if err != nil {
				t.Fatal(err)
			}
			if gotSandbox.State != domain.SandboxRunning {
				t.Fatalf("sandbox state after rollback = %s; want running", gotSandbox.State)
			}
			gotBoost, err := env.store.BoostStore().Get(t.Context(), sbx.SandboxID)
			if err != nil {
				t.Fatalf("boost not retained after operation create failure: %v", err)
			}
			if gotBoost.BoostID != boost.BoostID {
				t.Fatalf("retained boost id = %s; want %s", gotBoost.BoostID, boost.BoostID)
			}
		})
	}
}
