//go:build linux

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetect_OnTmpFS_FallsBackToCopy(t *testing.T) {
	got, err := Detect(t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Name() != "copy" {
		t.Errorf("Detect on tmpfs = %q, want %q", got.Name(), "copy")
	}
}

func TestDetect_MissingDir_ReturnsError(t *testing.T) {
	got, err := Detect(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatalf("expected error for missing dir")
	}
	if got != nil {
		t.Errorf("expected nil backend on err, got %v", got)
	}
}

func TestDetect_NotADir_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Detect(notDir)
	if err == nil {
		t.Fatalf("expected error when path is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory' message, got %v", err)
	}
}

func TestDetect_OnReflinkFS_ReturnsReflink(t *testing.T) {
	root := os.Getenv("NAVARIS_REFLINK_TEST_DIR")
	if root == "" {
		t.Skip("set NAVARIS_REFLINK_TEST_DIR to a CoW-capable mount to enable")
	}
	got, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got.Name() != "reflink" {
		t.Errorf("Detect on reflink FS = %q, want %q", got.Name(), "reflink")
	}
}

// Sanity: probeReflink must not leave temp files behind.
func TestProbeReflink_CleansUp(t *testing.T) {
	dir := t.TempDir()
	_ = probeReflink(dir) // ignore result, only care about cleanup

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		t.Errorf("probe file left behind: %s", e.Name())
	}
}
