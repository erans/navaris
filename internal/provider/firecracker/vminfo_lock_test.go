//go:build firecracker

package firecracker

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

func TestLockFor_LazyCreation(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	fl, err := p.lockFor("vm-1")
	if err != nil {
		t.Fatalf("lockFor: %v", err)
	}
	defer fl.mu.Unlock()

	// Same vmID returns the same lock object on a second call (after unlock).
	p.vmMu.Lock()
	fl2, ok := p.fileMu["vm-1"]
	p.vmMu.Unlock()
	if !ok || fl2 != fl {
		t.Fatalf("lazy-created lock not stored in fileMu")
	}
}

func TestLockFor_FailFastWhenStopped(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	// Simulate StopSandbox having flipped the sentinel.
	p.vmMu.Lock()
	fl := &vmFileLock{}
	fl.stopped.Store(true)
	p.fileMu["vm-2"] = fl
	p.vmMu.Unlock()

	_, err := p.lockFor("vm-2")
	if !errors.Is(err, domain.ErrVMStopped) {
		t.Fatalf("err = %v; want ErrVMStopped", err)
	}
}

func TestLockFor_SerializesConcurrentWriters(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}

	var (
		inFlight    int
		maxInFlight int
		mu          sync.Mutex
	)
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			fl, err := p.lockFor("vm-3")
			if err != nil {
				t.Errorf("lockFor: %v", err)
				return
			}
			defer fl.mu.Unlock()

			mu.Lock()
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			mu.Unlock()

			// Hold briefly to force overlap.
			// (no-op; scheduler contention is enough at N=50.)
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxInFlight != 1 {
		t.Errorf("max in-flight writers = %d; want 1 (must be serialized)", maxInFlight)
	}
}

func TestLockFor_RechecksStoppedAfterWaiting(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}
	fl := &vmFileLock{}
	p.fileMu["vm-gap"] = fl

	afterFastCheck := make(chan struct{})
	releaseHook := make(chan struct{})
	hookErr := make(chan string, 1)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseHook) })
	}
	p.lockForAfterFastCheckHook = func() {
		if !p.vmMu.TryLock() {
			hookErr <- "lockFor hook ran while vmMu was held"
		} else {
			p.vmMu.Unlock()
			hookErr <- ""
		}
		close(afterFastCheck)
		<-releaseHook
	}

	// Force lockFor past its fast check and up to the per-VM mutex wait.
	fl.mu.Lock()
	done := make(chan error, 1)
	go func() {
		got, err := p.lockFor("vm-gap")
		if got != nil {
			got.mu.Unlock()
		}
		done <- err
	}()

	select {
	case <-afterFastCheck:
	case <-time.After(time.Second):
		release()
		fl.mu.Unlock()
		t.Fatal("lockFor did not reach the post-fast-check barrier")
	}
	if msg := <-hookErr; msg != "" {
		release()
		fl.mu.Unlock()
		t.Fatal(msg)
	}

	fl.stopped.Store(true)
	release()
	fl.mu.Unlock()

	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrVMStopped) {
			t.Fatalf("lockFor error = %v; want ErrVMStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lockFor did not complete after sentinel transition")
	}
}

// TestLockFor_FailFastWhileStopHoldsLock verifies the fast path: a late
// writer must fail with ErrVMStopped WITHOUT blocking on fl.mu while
// StopSandbox holds it. (With a naive "acquire fl.mu then check stopped"
// design, this test would block for the full hold time and time out.)
func TestLockFor_FailFastWhileStopHoldsLock(t *testing.T) {
	p := &Provider{vms: map[string]*VMInfo{}, fileMu: map[string]*vmFileLock{}}
	p.vmMu.Lock()
	fl := &vmFileLock{}
	fl.stopped.Store(true)
	p.fileMu["vm-4"] = fl
	p.vmMu.Unlock()

	// Simulate StopSandbox holding fl.mu through a long teardown.
	fl.mu.Lock()
	defer fl.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := p.lockFor("vm-4")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrVMStopped) {
			t.Fatalf("err = %v; want ErrVMStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lockFor blocked on fl.mu while stopped; fail-fast broken")
	}
}
