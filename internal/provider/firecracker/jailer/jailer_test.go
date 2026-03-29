package jailer

import (
	"testing"
)

func TestUIDAllocator(t *testing.T) {
	a := NewUIDAllocator(10000)
	uid0 := a.Allocate()
	uid1 := a.Allocate()
	if uid0 != 10000 {
		t.Errorf("first UID: got %d, want 10000", uid0)
	}
	if uid1 != 10001 {
		t.Errorf("second UID: got %d, want 10001", uid1)
	}
}

func TestUIDAllocatorInitPast(t *testing.T) {
	a := NewUIDAllocator(10000)
	a.InitPast(10050)
	uid := a.Allocate()
	if uid <= 10050 {
		t.Errorf("expected UID > 10050, got %d", uid)
	}
}

func TestChrootPath(t *testing.T) {
	path := ChrootPath("/srv/firecracker", "nvrs-fc-a1b2c3d4")
	expected := "/srv/firecracker/firecracker/nvrs-fc-a1b2c3d4"
	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

func TestVMInfoPath(t *testing.T) {
	path := VMInfoPath("/srv/firecracker", "nvrs-fc-a1b2c3d4")
	expected := "/srv/firecracker/firecracker/nvrs-fc-a1b2c3d4/vminfo.json"
	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}
