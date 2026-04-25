//go:build firecracker

package firecracker

import (
	"strings"
	"testing"
)

func TestNew_RequiresStorageRegistry(t *testing.T) {
	cfg := Config{
		FirecrackerBin: "/bin/true",
		KernelPath:     "/dev/null",
		ImageDir:       t.TempDir(),
		ChrootBase:     t.TempDir(),
		SnapshotDir:    t.TempDir(),
		// Storage intentionally nil
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatalf("expected error when Storage is nil")
	}
	if !strings.Contains(err.Error(), "Storage") {
		t.Errorf("expected error mentioning Storage, got: %v", err)
	}
}
