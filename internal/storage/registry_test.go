package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistry_For_LongestPrefixWins(t *testing.T) {
	r := NewRegistry()
	r.Set("/srv/firecracker", CopyBackend{})
	r.Set("/srv/firecracker/snapshots", ReflinkBackend{})

	if got := r.For("/srv/firecracker/snapshots/abc/rootfs.ext4"); got.Name() != "reflink" {
		t.Errorf("longest prefix should win: got %q", got.Name())
	}
	if got := r.For("/srv/firecracker/vms/xyz/rootfs.ext4"); got.Name() != "copy" {
		t.Errorf("shorter prefix should win when no longer match: got %q", got.Name())
	}
}

func TestRegistry_For_FallbackBackend(t *testing.T) {
	r := NewRegistry()
	r.SetFallback(CopyBackend{})
	if got := r.For("/some/unmapped/path"); got.Name() != "copy" {
		t.Errorf("unmapped path should hit fallback: got %q", got.Name())
	}
}

func TestRegistry_For_NoFallback_PanicsOnLookup(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic when no fallback is set and no prefix matches")
		}
	}()
	_ = r.For("/x")
}

func TestRegistry_BuildFromMode_Auto_WiresProbedRoots(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	r, err := BuildRegistry(Config{Mode: ModeAuto}, roots, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	for _, root := range roots {
		got := r.For(filepath.Join(root, "anyfile"))
		if got.Name() != "copy" {
			t.Errorf("auto mode on tmpfs root %s: got %q, want copy", root, got.Name())
		}
	}
}

func TestRegistry_BuildFromMode_ExplicitCopy(t *testing.T) {
	r, err := BuildRegistry(Config{Mode: ModeCopy}, []string{t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if r.For("/anything").Name() != "copy" {
		t.Errorf("explicit copy mode must produce copy")
	}
}

func TestRegistry_BuildFromMode_ExplicitReflink_OnTmpfsFails(t *testing.T) {
	_, err := BuildRegistry(Config{Mode: ModeReflink}, []string{t.TempDir()}, nil)
	if err == nil {
		t.Fatalf("explicit reflink on tmpfs must fail at startup")
	}
	if !strings.Contains(err.Error(), "reflink") {
		t.Errorf("expected error mentioning reflink, got %v", err)
	}
}

func TestRegistry_BuildFromMode_PerRootOverrideWins(t *testing.T) {
	root := t.TempDir()
	overrides := map[string]Mode{root: ModeCopy}
	r, err := BuildRegistry(Config{Mode: ModeAuto}, []string{root}, overrides)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if got := r.For(filepath.Join(root, "x")); got.Name() != "copy" {
		t.Errorf("override should force copy, got %q", got.Name())
	}
}

func TestRegistry_BuildFromMode_StubBackendsFail(t *testing.T) {
	for _, mode := range []Mode{ModeBtrfsSubvol, ModeZfs} {
		_, err := BuildRegistry(Config{Mode: mode}, []string{t.TempDir()}, nil)
		if err == nil {
			t.Errorf("explicit %s mode must fail (stub not wired)", mode)
		}
	}
}

func TestRegistry_BuildFromMode_UnknownModeFails(t *testing.T) {
	_, err := BuildRegistry(Config{Mode: Mode("nonsense")}, []string{t.TempDir()}, nil)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestRegistry_CloneFile_FallsBackOnErrUnsupported(t *testing.T) {
	// Register ReflinkBackend for the same directory the src/dst live in,
	// so r.For(dst) resolves to ReflinkBackend. tmpfs makes ReflinkBackend
	// return ErrUnsupported at op time; CloneFile must fall back to copy.
	dir := t.TempDir()
	r := NewRegistry()
	r.SetFallback(CopyBackend{})
	r.Set(dir, ReflinkBackend{})

	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	used, err := r.CloneFile(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("CloneFile: %v", err)
	}
	if used.Name() != "copy" {
		t.Errorf("expected fallback to copy, got %q", used.Name())
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "hello" {
		t.Errorf("dst = %q (err=%v), want hello", got, err)
	}
}

func TestRegistry_CloneFile_NonUnsupportedErrorPropagates(t *testing.T) {
	// Registry resolves to CopyBackend; pass a missing src so copy errors
	// with something OTHER than ErrUnsupported. The fallback chain must
	// NOT swallow such errors — it propagates.
	r := NewRegistry()
	r.SetFallback(CopyBackend{})
	dir := t.TempDir()
	r.Set(dir, CopyBackend{})

	missing := filepath.Join(dir, "missing")
	dst := filepath.Join(dir, "dst")
	used, err := r.CloneFile(context.Background(), missing, dst)
	if err == nil {
		t.Fatalf("expected error from missing src")
	}
	if errors.Is(err, ErrUnsupported) {
		t.Errorf("non-unsupported error must not be wrapped as ErrUnsupported: %v", err)
	}
	_ = used
}

func TestRegistry_BuildFromMode_EmptyOverrideUsesGlobal(t *testing.T) {
	root := t.TempDir()
	overrides := map[string]Mode{root: ""} // empty override = "no override"
	r, err := BuildRegistry(Config{Mode: ModeCopy}, []string{root}, overrides)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	// Empty override should NOT shadow global Mode; resolved backend must
	// be copy (from global), not the result of probing.
	if got := r.For(filepath.Join(root, "x")); got.Name() != "copy" {
		t.Errorf("empty override should let global mode win, got %q", got.Name())
	}
}
