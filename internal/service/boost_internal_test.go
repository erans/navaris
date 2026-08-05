package service

import (
	"sync"
	"testing"
)

func TestLockSandbox_LazyCreation(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sync.Mutex{}}
	bs.mu.Lock()
	m1 := bs.lockSandbox("sbx-1")
	m2 := bs.lockSandbox("sbx-1")
	bs.mu.Unlock()

	if m1 != m2 {
		t.Fatalf("lockSandbox returned different mutexes for the same sandbox")
	}
	bs.mu.Lock()
	m3 := bs.lockSandbox("sbx-2")
	bs.mu.Unlock()
	if m1 == m3 {
		t.Fatalf("lockSandbox returned the same mutex for different sandboxes")
	}
}
