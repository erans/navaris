package network

import (
	"testing"
)

func TestAllocatorFirstSubnet(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	ip := a.HostIP(idx)
	guest := a.GuestIP(idx)
	mask := a.Mask()

	if ip.String() != "172.26.0.1" {
		t.Errorf("host IP: got %s, want 172.26.0.1", ip)
	}
	if guest.String() != "172.26.0.2" {
		t.Errorf("guest IP: got %s, want 172.26.0.2", guest)
	}
	if mask.String() != "255.255.255.252" {
		t.Errorf("mask: got %s, want 255.255.255.252", mask)
	}
}

func TestAllocatorSequential(t *testing.T) {
	a := NewAllocator()
	idx0 := a.Allocate()
	idx1 := a.Allocate()
	idx2 := a.Allocate()

	if a.GuestIP(idx0).String() != "172.26.0.2" {
		t.Error("wrong guest IP for idx 0")
	}
	if a.GuestIP(idx1).String() != "172.26.0.6" {
		t.Error("wrong guest IP for idx 1")
	}
	if a.GuestIP(idx2).String() != "172.26.0.10" {
		t.Error("wrong guest IP for idx 2")
	}
}

func TestAllocatorRelease(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	a.Release(idx)
	if a.InUse(idx) {
		t.Error("expected idx released")
	}
}

func TestAllocatorInitPast(t *testing.T) {
	a := NewAllocator()
	a.InitPast(100)
	idx := a.Allocate()
	if idx <= 100 {
		t.Errorf("expected idx > 100, got %d", idx)
	}
}

func TestAllocatorBootArg(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	arg := a.KernelBootArg(idx)
	expected := "ip=172.26.0.2::172.26.0.1:255.255.255.252::eth0:off"
	if arg != expected {
		t.Errorf("boot arg:\ngot  %s\nwant %s", arg, expected)
	}
}
