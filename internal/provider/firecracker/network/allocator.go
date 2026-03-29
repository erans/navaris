package network

import (
	"fmt"
	"net"
	"sync"
)

type Allocator struct {
	mu    sync.Mutex
	next  int
	inUse map[int]bool
}

func NewAllocator() *Allocator {
	return &Allocator{inUse: make(map[int]bool)}
}

func (a *Allocator) InitPast(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if idx >= a.next {
		a.next = idx + 1
	}
}

func (a *Allocator) Allocate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := a.next
	a.next++
	a.inUse[idx] = true
	return idx
}

func (a *Allocator) Release(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inUse, idx)
}

func (a *Allocator) InUse(idx int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inUse[idx]
}

func base(idx int) net.IP {
	offset := idx * 4
	return net.IPv4(172, 26, byte(offset>>8), byte(offset&0xFF))
}

func (a *Allocator) HostIP(idx int) net.IP {
	ip := base(idx)
	ip[15]++
	return ip
}

func (a *Allocator) GuestIP(idx int) net.IP {
	ip := base(idx)
	ip[15] += 2
	return ip
}

func (a *Allocator) Mask() net.IP {
	m := net.CIDRMask(30, 32)
	return net.IPv4(m[0], m[1], m[2], m[3])
}

func (a *Allocator) KernelBootArg(idx int) string {
	return fmt.Sprintf("ip=%s::%s:%s::eth0:off",
		a.GuestIP(idx), a.HostIP(idx),
		a.Mask())
}
