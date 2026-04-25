package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestErrUnsupported_Is(t *testing.T) {
	wrapped := fmt.Errorf("ctx: %v", ErrUnsupported)
	if errors.Is(wrapped, ErrUnsupported) {
		t.Errorf("%%v wrapping must not establish an errors.Is chain")
	}
	wrapped2 := errors.Join(ErrUnsupported, errors.New("zfs not configured"))
	if !errors.Is(wrapped2, ErrUnsupported) {
		t.Errorf("errors.Join should preserve ErrUnsupported")
	}
}

func TestCapabilitiesZeroValue(t *testing.T) {
	var c Capabilities
	if c.InstantClone || c.SharesBlocks || c.RequiresSameFS {
		t.Errorf("zero Capabilities must be all-false")
	}
}

// Compile-time: CopyBackend must satisfy Backend.
var _ Backend = (*CopyBackend)(nil)

func TestCopyBackend_CloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	payload := bytes.Repeat([]byte("a"), 1<<20) // 1 MB
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &CopyBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("dst contents differ from src")
	}

	// Divergence: writing to src does not affect dst, and vice versa.
	if err := os.WriteFile(src, []byte("changed"), 0o644); err != nil {
		t.Fatalf("rewrite src: %v", err)
	}
	got2, _ := os.ReadFile(dst)
	if !bytes.Equal(got2, payload) {
		t.Errorf("dst was affected by src write (not independent)")
	}
}

func TestCopyBackend_NoPartialOnError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing")
	dst := filepath.Join(dir, "dst")
	b := &CopyBackend{}
	err := b.CloneFile(context.Background(), src, dst)
	if err == nil {
		t.Fatal("expected error from missing src")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("dst must not exist after failed clone, got stat err=%v", statErr)
	}
}

func TestCopyBackend_RenameFailureCleansTmp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// dst is in a subdirectory that does not exist — rename will fail.
	dst := filepath.Join(dir, "missing-subdir", "dst")

	b := &CopyBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err == nil {
		t.Fatalf("expected error when dst parent does not exist")
	}

	// dst.tmp must NOT remain in the parent dir of the resolved tmp path
	// (which is under dir/missing-subdir, but since that dir doesn't exist
	// the tmp couldn't be created either — verify nothing leaked into dir).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "src" {
			t.Errorf("unexpected leftover in dir: %s", e.Name())
		}
	}
}

func TestCopyBackend_OEXCL_RejectsConcurrentTmp(t *testing.T) {
	// Pre-existing dst.tmp from a "prior run" must be cleaned up by the
	// implementation, not silently clobbered. After CloneFile returns,
	// dst exists with the new content and no .tmp remains.
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst+".tmp", []byte("STALE"), 0o644); err != nil {
		t.Fatalf("plant stale tmp: %v", err)
	}
	b := &CopyBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "new" {
		t.Errorf("dst content = %q (err=%v), want %q", got, err, "new")
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("dst.tmp must not exist after success, stat err=%v", err)
	}
}

func TestCopyBackend_NameAndCapabilities(t *testing.T) {
	b := &CopyBackend{}
	if b.Name() != "copy" {
		t.Errorf("Name = %q, want %q", b.Name(), "copy")
	}
	caps := b.Capabilities()
	if caps.InstantClone || caps.SharesBlocks || caps.RequiresSameFS {
		t.Errorf("CopyBackend caps must be all-false, got %+v", caps)
	}
}

// Compile-time: ReflinkBackend must satisfy Backend.
var _ Backend = (*ReflinkBackend)(nil)

func TestReflinkBackend_Name(t *testing.T) {
	// Name is platform-stable: "reflink" on Linux and on the non-Linux stub.
	b := &ReflinkBackend{}
	if b.Name() != "reflink" {
		t.Errorf("Name = %q, want %q", b.Name(), "reflink")
	}
}

// TestReflinkBackend_CloneFile_OnReflinkFS exercises the real ioctl on a
// filesystem that supports it. Skip when no such directory is configured.
// Set NAVARIS_REFLINK_TEST_DIR=/path on a btrfs/XFS-reflink mount to enable.
func TestReflinkBackend_CloneFile_OnReflinkFS(t *testing.T) {
	root := os.Getenv("NAVARIS_REFLINK_TEST_DIR")
	if root == "" {
		t.Skip("set NAVARIS_REFLINK_TEST_DIR to a CoW-capable mount to enable")
	}
	dir, err := os.MkdirTemp(root, "reflink-test-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	payload := bytes.Repeat([]byte("z"), 4<<20) // 4 MB
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	b := &ReflinkBackend{}
	if err := b.CloneFile(context.Background(), src, dst); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("dst contents differ")
	}
}

func TestReflinkBackend_CloneFile_OnTmpFS_FailsClean(t *testing.T) {
	dir := t.TempDir() // tmpfs in CI; ioctl FICLONE returns EOPNOTSUPP/ENOTTY
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	b := &ReflinkBackend{}
	err := b.CloneFile(context.Background(), src, dst)
	if err == nil {
		t.Fatalf("expected error on non-CoW FS")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported wrap, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Errorf("dst must not exist after failed clone")
	}
}
