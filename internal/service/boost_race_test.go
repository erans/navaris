package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/service"
)

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
	<-applyCh // Start(A) apply started (cpu=2); let it finish.
	releaseCh <- struct{}{}
	select {
	case err := <-doneA:
		if err != nil {
			t.Fatalf("Start(A): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start(A) did not complete")
	}

	// Launch expire(A) in a goroutine (fire the timer). Its revert apply starts
	// and blocks on releaseCh, holding the per-sandbox lock (with the fix).
	go clk.fire(61 * time.Second)
	<-applyCh // expire(A) revert started; it now holds the per-sandbox lock.

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
	select {
	case <-applyCh:
		t.Fatal("Start(B) applied concurrently with expire(A); per-sandbox lock did not serialize (F7 not fixed)")
	case <-time.After(100 * time.Millisecond):
		// Good: Start(B) is blocked on the per-sandbox lock.
	}

	// Release expire(A); it finishes and releases the per-sandbox lock.
	// Start(B) then acquires the lock and applies cpu=4.
	releaseCh <- struct{}{}
	select {
	case cpu := <-applyCh:
		if cpu != 4 {
			t.Fatalf("after expire release, apply cpu = %d; want 4 (B)", cpu)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start(B) did not apply after expire released")
	}
	releaseCh <- struct{}{} // let Start(B) finish
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start(B): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start(B) did not complete")
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
	callA := <-entered
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

	time.Sleep(50 * time.Millisecond)
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
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	callB := <-entered
	if callB.cpu != cpuB {
		t.Fatalf("second apply cpu=%d; want %d", callB.cpu, cpuB)
	}
	close(callB.release)
	if err := <-doneB; err != nil {
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
			SandboxID: a.SandboxID, CPULimit: &cpuA, DurationSeconds: 60})
		done <- err
	}()
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: b.SandboxID, CPULimit: &cpuB, DurationSeconds: 60})
		done <- err
	}()

	var releases []chan struct{}
	for len(releases) < 2 {
		select {
		case release := <-entered:
			releases = append(releases, release)
		case <-time.After(time.Second):
			t.Fatal("different sandboxes did not enter apply concurrently")
		}
	}
	for _, release := range releases {
		close(release)
	}
	for range 2 {
		if err := <-done; err != nil {
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
	cancelCall := <-entered

	cpuB := 8
	startDone := make(chan error, 1)
	go func() {
		_, err := env.boost.Start(context.Background(), service.StartBoostOpts{
			SandboxID: sbx.SandboxID, CPULimit: &cpuB, DurationSeconds: 60,
		})
		startDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
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
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	startCall := <-entered
	if startCall.cpu != cpuB {
		t.Fatalf("post-Cancel Start cpu=%d; want %d", startCall.cpu, cpuB)
	}
	close(startCall.release)
	if err := <-startDone; err != nil {
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
