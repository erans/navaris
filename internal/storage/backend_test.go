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
