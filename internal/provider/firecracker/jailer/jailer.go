package jailer

import (
	"fmt"
	"path/filepath"
	"sync"
)

type UIDAllocator struct {
	mu   sync.Mutex
	next int
}

func NewUIDAllocator(baseUID int) *UIDAllocator {
	return &UIDAllocator{next: baseUID}
}

func (a *UIDAllocator) InitPast(uid int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if uid >= a.next {
		a.next = uid + 1
	}
}

func (a *UIDAllocator) Allocate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid := a.next
	a.next++
	return uid
}

func ChrootPath(base, vmID string) string {
	return filepath.Join(base, "firecracker", vmID)
}

func VMInfoPath(base, vmID string) string {
	return filepath.Join(ChrootPath(base, vmID), "vminfo.json")
}

func VMDirGlob(base string) string {
	return filepath.Join(base, "firecracker", "nvrs-fc-*")
}

type Config struct {
	FirecrackerBin string
	JailerBin      string
	VMID           string
	UID            int
	GID            int
	ChrootBase     string
	KernelPath     string
}

func (c *Config) ChrootDir() string {
	return ChrootPath(c.ChrootBase, c.VMID)
}

func (c *Config) String() string {
	return fmt.Sprintf("jailer{vm=%s uid=%d chroot=%s}", c.VMID, c.UID, c.ChrootDir())
}
