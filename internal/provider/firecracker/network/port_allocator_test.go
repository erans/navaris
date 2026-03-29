package network

import (
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestPortAllocatorFirstPort(t *testing.T) {
	a := NewPortAllocator()
	port, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if port != 40000 {
		t.Errorf("got %d, want 40000", port)
	}
}

func TestPortAllocatorSequential(t *testing.T) {
	a := NewPortAllocator()
	p1, _ := a.Allocate()
	p2, _ := a.Allocate()
	p3, _ := a.Allocate()

	if p1 != 40000 || p2 != 40001 || p3 != 40002 {
		t.Errorf("got %d %d %d, want 40000 40001 40002", p1, p2, p3)
	}
}

func TestPortAllocatorRelease(t *testing.T) {
	a := NewPortAllocator()
	port, _ := a.Allocate()
	a.Release(port)

	// Next allocation should still advance (not reuse immediately).
	p2, _ := a.Allocate()
	if p2 != 40001 {
		t.Errorf("got %d, want 40001", p2)
	}
}

func TestPortAllocatorMarkUsed(t *testing.T) {
	a := NewPortAllocator()
	a.MarkUsed(40000)
	port, _ := a.Allocate()
	if port != 40001 {
		t.Errorf("got %d, want 40001 (40000 marked used)", port)
	}
}

func TestPortAllocatorCapacityExceeded(t *testing.T) {
	a := NewPortAllocator()
	// Mark all ports as used.
	for p := portMin; p <= portMax; p++ {
		a.MarkUsed(p)
	}

	_, err := a.Allocate()
	if !errors.Is(err, domain.ErrCapacityExceeded) {
		t.Errorf("got %v, want ErrCapacityExceeded", err)
	}
}

func TestPortAllocatorWrapAround(t *testing.T) {
	a := NewPortAllocator()
	// Allocate up to the last port.
	for i := portMin; i < portMax; i++ {
		a.MarkUsed(i)
	}
	// next is at portMin, all used except portMax.
	port, err := a.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if port != portMax {
		t.Errorf("got %d, want %d", port, portMax)
	}
}
