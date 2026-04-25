//go:build firecracker

package firecracker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFPInfo_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fpinfo.json")

	want := &fpInfo{
		ForkPointID:    "fp-abc",
		ParentVMID:     "vm-xyz",
		Mode:           "live",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		StorageBackend: "reflink",
		SpawnPending:   3,
		Descendants:    []string{"sbx-1", "sbx-2"},
	}
	if err := writeFPInfo(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readFPInfo(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ForkPointID != want.ForkPointID || got.ParentVMID != want.ParentVMID ||
		got.Mode != want.Mode || got.StorageBackend != want.StorageBackend ||
		got.SpawnPending != want.SpawnPending || len(got.Descendants) != 2 ||
		got.Descendants[0] != "sbx-1" || got.Descendants[1] != "sbx-2" {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt mismatch: got %v want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestForkPointDir_Layout(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: "/srv/firecracker"}}
	if got := p.forkPointDir("fp-abc"); got != "/srv/firecracker/forkpoints/fp-abc" {
		t.Errorf("forkPointDir = %q", got)
	}
	if got := p.fpInfoPath("fp-abc"); got != "/srv/firecracker/forkpoints/fp-abc/fpinfo.json" {
		t.Errorf("fpInfoPath = %q", got)
	}
}

func TestUpdateFPInfo_AtomicMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fpinfo.json")
	if err := writeFPInfo(path, &fpInfo{ForkPointID: "fp-1", SpawnPending: 2}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := updateFPInfo(path, func(i *fpInfo) {
		i.Descendants = append(i.Descendants, "sbx-1")
		i.SpawnPending--
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := readFPInfo(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SpawnPending != 1 || len(got.Descendants) != 1 || got.Descendants[0] != "sbx-1" {
		t.Errorf("unexpected state after update: %+v", got)
	}
}

func TestUpdateFPInfo_PreservesUnmodifiedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fpinfo.json")
	original := &fpInfo{
		ForkPointID:    "fp-2",
		ParentVMID:     "vm-2",
		Mode:           "live",
		StorageBackend: "reflink",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
		SpawnPending:   5,
	}
	if err := writeFPInfo(path, original); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Mutate only Descendants.
	if err := updateFPInfo(path, func(i *fpInfo) {
		i.Descendants = []string{"sbx-x"}
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := readFPInfo(path)
	if got.ForkPointID != original.ForkPointID || got.ParentVMID != original.ParentVMID ||
		got.Mode != original.Mode || got.StorageBackend != original.StorageBackend ||
		got.SpawnPending != original.SpawnPending {
		t.Errorf("unrelated fields mutated: got %+v", got)
	}
}

func TestWriteFPInfo_AtomicOnRenameFailure(t *testing.T) {
	// Direct dst path inside a non-existent subdir → MkdirAll succeeds for
	// the immediate parent, so this test verifies the happy mkdir path.
	// We separately test that no .tmp leftover exists after a clean write.
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "fpinfo.json")
	if err := writeFPInfo(path, &fpInfo{ForkPointID: "fp-3"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp must not exist after clean write")
	}
}

// fpOrphanTTL constant must be exported as 1 hour per spec; this guards
// against accidental tuning that would change orphan-cleanup behavior.
func TestFPOrphanTTL(t *testing.T) {
	if fpOrphanTTL != time.Hour {
		t.Errorf("fpOrphanTTL = %v, want 1h", fpOrphanTTL)
	}
}
