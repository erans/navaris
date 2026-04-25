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

func TestReleaseForkPointDescendant_GCsWhenEmpty(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-test1"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID:  fpID,
		Descendants:  []string{"sbx-1"},
		SpawnPending: 0,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := p.ReleaseForkPointDescendant(fpID, "sbx-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(fpDir); !os.IsNotExist(err) {
		t.Errorf("fork-point dir should be GC'd after last descendant; stat err=%v", err)
	}
}

func TestReleaseForkPointDescendant_KeepsWhenOthersRemain(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-test2"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID: fpID,
		Descendants: []string{"sbx-1", "sbx-2"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := p.ReleaseForkPointDescendant(fpID, "sbx-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(fpDir); err != nil {
		t.Errorf("fork-point dir should remain while sbx-2 is alive: %v", err)
	}
	got, _ := readFPInfo(p.fpInfoPath(fpID))
	if len(got.Descendants) != 1 || got.Descendants[0] != "sbx-2" {
		t.Errorf("expected only sbx-2, got %v", got.Descendants)
	}
}

func TestReleaseForkPointDescendant_KeepsWhenSpawnPending(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-test3"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID:  fpID,
		Descendants:  []string{"sbx-1"},
		SpawnPending: 2, // 2 children still being spawned
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := p.ReleaseForkPointDescendant(fpID, "sbx-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(fpDir); err != nil {
		t.Errorf("fork-point dir should remain while spawns are pending: %v", err)
	}
}

func TestReleaseForkPointDescendant_IdempotentOnMissingFP(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	// fpInfoPath does not exist — Release must return nil (already GC'd).
	if err := p.ReleaseForkPointDescendant("fp-gone", "sbx-1"); err != nil {
		t.Errorf("expected nil for missing fork-point, got %v", err)
	}
}

func TestReleaseForkPointDescendant_UnknownVMIDIsNoOp(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-test5"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID: fpID,
		Descendants: []string{"sbx-1"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := p.ReleaseForkPointDescendant(fpID, "sbx-NOPE"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	got, _ := readFPInfo(p.fpInfoPath(fpID))
	if len(got.Descendants) != 1 {
		t.Errorf("descendants should be unchanged when removing unknown vmID, got %v", got.Descendants)
	}
}

func TestRecoverForkPoints_GCsOrphanForkpoints(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}

	// Orphan: SpawnPending > 0, CreatedAt older than fpOrphanTTL.
	orphanID := "fp-orphan"
	orphanDir := p.forkPointDir(orphanID)
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(orphanID), &fpInfo{
		ForkPointID:  orphanID,
		SpawnPending: 2,
		CreatedAt:    time.Now().UTC().Add(-2 * fpOrphanTTL),
	}); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	// Live: has descendants, no spawn pending, recently created.
	liveID := "fp-live"
	liveDir := p.forkPointDir(liveID)
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatalf("mkdir live: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(liveID), &fpInfo{
		ForkPointID: liveID,
		Descendants: []string{"sbx-1"},
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("write live: %v", err)
	}

	if err := p.recoverForkPoints(); err != nil {
		t.Fatalf("recoverForkPoints: %v", err)
	}

	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("orphan should have been GC'd, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(liveDir, "fpinfo.json")); err != nil {
		t.Errorf("live fork-point should remain: %v", err)
	}
}

func TestRecoverForkPoints_GCsUnreferenced(t *testing.T) {
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-unref"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID: fpID,
		// Descendants empty, SpawnPending = 0 → unreferenced
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := p.recoverForkPoints(); err != nil {
		t.Fatalf("recoverForkPoints: %v", err)
	}
	if _, err := os.Stat(fpDir); !os.IsNotExist(err) {
		t.Errorf("unreferenced fork-point should have been GC'd")
	}
}

func TestRecoverForkPoints_PreservesYoungOrphan(t *testing.T) {
	// SpawnPending > 0 but CreatedAt within TTL → not yet an orphan.
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-young"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFPInfo(p.fpInfoPath(fpID), &fpInfo{
		ForkPointID:  fpID,
		SpawnPending: 1,
		CreatedAt:    time.Now().UTC().Add(-time.Minute), // far younger than fpOrphanTTL
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := p.recoverForkPoints(); err != nil {
		t.Fatalf("recoverForkPoints: %v", err)
	}
	if _, err := os.Stat(fpDir); err != nil {
		t.Errorf("young orphan should NOT have been GC'd: %v", err)
	}
}

func TestRecoverForkPoints_NoForkPointsDir(t *testing.T) {
	// recover should be a no-op (and not error) when forkpoints dir doesn't exist.
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	if err := p.recoverForkPoints(); err != nil {
		t.Errorf("recoverForkPoints with no dir should be no-op, got %v", err)
	}
}

func TestRecoverForkPoints_IgnoresUnreadable(t *testing.T) {
	// Fork-point dir with no fpinfo.json: must NOT be deleted (operator
	// can investigate). The function must not fail.
	p := &Provider{config: Config{ChrootBase: t.TempDir()}}
	fpID := "fp-broken"
	fpDir := p.forkPointDir(fpID)
	if err := os.MkdirAll(fpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := p.recoverForkPoints(); err != nil {
		t.Errorf("recoverForkPoints with unreadable fp should not error, got %v", err)
	}
	if _, err := os.Stat(fpDir); err != nil {
		t.Errorf("unreadable fork-point should be left alone: %v", err)
	}
}
