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

	applyCh := make(chan int)       // signals an apply started (cpu value)
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
