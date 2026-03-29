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

	infos, errs := ScanVMDirs(base)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(infos))
	}
}

func TestSubnetIdxZeroPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:        "nvrs-fc-zeroidx",
		CID:       100,
		UID:       10000,
		SubnetIdx: 0,
		TapDevice: "fc-zeroidx",
	}
	if err := info.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := ReadVMInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SubnetIdx != 0 {
		t.Errorf("SubnetIdx=0 not persisted, got %d", got.SubnetIdx)
	}
	if got.TapDevice != "fc-zeroidx" {
		t.Errorf("TapDevice mismatch: %s", got.TapDevice)
	}
}

func TestReadVMInfoMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	os.WriteFile(path, []byte("{corrupted"), 0o600)
	_, err := ReadVMInfo(path)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestReadVMInfoMissing(t *testing.T) {
	_, err := ReadVMInfo("/tmp/nonexistent-vminfo-test.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestScanVMDirsWithCorruptEntry(t *testing.T) {
	base := t.TempDir()
	fcDir := filepath.Join(base, "firecracker")

	// Create one valid VM.
	goodDir := filepath.Join(fcDir, "nvrs-fc-goodgood")
	os.MkdirAll(goodDir, 0o755)
	good := &VMInfo{ID: "nvrs-fc-goodgood", CID: 100, UID: 10000}
	good.Write(filepath.Join(goodDir, "vminfo.json"))

	// Create one corrupt VM.
	badDir := filepath.Join(fcDir, "nvrs-fc-badbadbb")
	os.MkdirAll(badDir, 0o755)
	os.WriteFile(filepath.Join(badDir, "vminfo.json"), []byte("{corrupt"), 0o600)

	infos, errs := ScanVMDirs(base)
	if len(infos) != 1 {
		t.Errorf("expected 1 valid VM, got %d", len(infos))
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error for corrupt VM, got %d", len(errs))
	}
}
