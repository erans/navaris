package network

import (
	"sync"

	"github.com/navaris/navaris/internal/domain"
)

const (
	portMin = 40000
	portMax = 49999
)

// PortAllocator manages host port allocation for port forwarding.
type PortAllocator struct {
	mu   sync.Mutex
	used map[int]bool
	next int
}

// NewPortAllocator creates a PortAllocator starting at portMin.
func NewPortAllocator() *PortAllocator {
	return &PortAllocator{
		used: make(map[int]bool),
		next: portMin,
	}
}

// Allocate returns the next available host port.
func (a *PortAllocator) Allocate() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Normalize before capturing start so the sentinel comparison is valid.
	if a.next > portMax {
		a.next = portMin
	}
	// Scan from next through the range, wrapping once.
	start := a.next
	for {
		if !a.used[a.next] {
			port := a.next
			a.used[port] = true
			a.next++
			return port, nil
		}
		a.next++
		if a.next > portMax {
			a.next = portMin
		}
		if a.next == start {
			return 0, domain.ErrCapacityExceeded
		}
	}
}

// Release returns a port to the pool.
func (a *PortAllocator) Release(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, port)
}

// MarkUsed marks a port as in use (for recovery).
func (a *PortAllocator) MarkUsed(port int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.used[port] = true
}
