package service

import (
	"testing"
	"time"
)

func TestAcquireSandbox_ReferenceLifetime(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sandboxLock{}}
	release1 := bs.acquireSandbox("sbx-1")

	acquired2 := make(chan func(), 1)
	go func() { acquired2 <- bs.acquireSandbox("sbx-1") }()

	deadline := time.Now().Add(time.Second)
	for {
		bs.mu.Lock()
		entry := bs.sbxLocks["sbx-1"]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		bs.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiter was not reference-counted; refs=%d", refs)
		}
		time.Sleep(time.Millisecond)
	}

	release1()
	release2 := <-acquired2
	bs.mu.Lock()
	entry, ok := bs.sbxLocks["sbx-1"]
	if !ok || entry.refs != 1 {
		bs.mu.Unlock()
		t.Fatalf("entry after first release = %#v; want refs=1", entry)
	}
	bs.mu.Unlock()

	release2()
	bs.mu.Lock()
	_, ok = bs.sbxLocks["sbx-1"]
	bs.mu.Unlock()
	if ok {
		t.Fatal("idle sandbox lock entry was not reclaimed")
	}
}

func TestAcquireSandbox_DifferentSandboxesDoNotBlock(t *testing.T) {
	bs := &BoostService{timers: map[string]Timer{}, sbxLocks: map[string]*sandboxLock{}}
	release1 := bs.acquireSandbox("sbx-1")
	defer release1()

	done := make(chan func(), 1)
	go func() { done <- bs.acquireSandbox("sbx-2") }()
	select {
	case release2 := <-done:
		release2()
	case <-time.After(time.Second):
		t.Fatal("different sandbox blocked on sbx-1")
	}
}
