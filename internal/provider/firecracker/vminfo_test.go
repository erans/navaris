package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVMInfoWriteRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:        "nvrs-fc-abc12345",
		PID:       12345,
		CID:       100,
		TapDevice: "fc-abc12345",
		SubnetIdx: 3,
		UID:       10003,
	}

	if err := info.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := ReadVMInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != info.ID || got.PID != info.PID || got.CID != info.CID {
		t.Errorf("mismatch: got %+v", got)
	}
	if got.TapDevice != info.TapDevice || got.SubnetIdx != info.SubnetIdx {
		t.Errorf("mismatch: got %+v", got)
	}
}

func TestVMInfoClearRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:        "nvrs-fc-abc12345",
		PID:       12345,
		CID:       100,
		TapDevice: "fc-abc12345",
		SubnetIdx: 3,
		UID:       10003,
	}
	info.Write(path)

	info.ClearRuntime()
	info.Write(path)

	got, _ := ReadVMInfo(path)
	if got.PID != 0 || got.TapDevice != "" || got.SubnetIdx != 0 {
		t.Errorf("expected runtime fields cleared, got %+v", got)
	}
	if got.CID != 100 || got.UID != 10003 {
		t.Errorf("expected persistent fields preserved, got %+v", got)
	}
}

func TestScanVMDirs(t *testing.T) {
	base := t.TempDir()
	fcDir := filepath.Join(base, "firecracker")
	os.MkdirAll(fcDir, 0o755)

	for _, vm := range []struct {
		id  string
		cid uint32
		uid int
	}{
		{"nvrs-fc-aaaaaaaa", 100, 10000},
		{"nvrs-fc-bbbbbbbb", 105, 10005},
	} {
		dir := filepath.Join(fcDir, vm.id)
		os.MkdirAll(dir, 0o755)
		info := &VMInfo{ID: vm.id, CID: vm.cid, UID: vm.uid}
		info.Write(filepath.Join(dir, "vminfo.json"))
	}

	infos, err := ScanVMDirs(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(infos))
	}
}
