//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/storage"
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

func TestSnapInfo_StorageBackendField_RoundTrip(t *testing.T) {
	si := &snapInfo{
		ID:             "snap-1",
		SourceVM:       "vm-1",
		Mode:           "stopped",
		StorageBackend: "reflink",
	}
	// Marshal/unmarshal preserves the field.
	data, err := json.Marshal(si)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"storage_backend":"reflink"`) {
		t.Errorf("expected storage_backend in JSON, got: %s", data)
	}
	var out snapInfo
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.StorageBackend != "reflink" {
		t.Errorf("StorageBackend = %q, want reflink", out.StorageBackend)
	}
}

func TestImageInfo_StorageBackendField_RoundTrip(t *testing.T) {
	ii := &imageInfo{
		Ref:            "img-1",
		Name:           "n",
		StorageBackend: "copy",
	}
	data, err := json.Marshal(ii)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"storage_backend":"copy"`) {
		t.Errorf("expected storage_backend in JSON, got: %s", data)
	}
}

func TestCloneFile_Wrapper_Smoke(t *testing.T) {
	dir := t.TempDir()
	reg := storage.NewRegistry()
	reg.SetFallback(storage.CopyBackend{})

	p := &Provider{config: Config{ChrootBase: dir}}
	p.storage = reg

	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	b, err := p.cloneFile(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("cloneFile: %v", err)
	}
	if b == nil || b.Name() != "copy" {
		t.Errorf("expected copy backend, got %v", b)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "data" {
		t.Errorf("dst = %q (err=%v)", got, err)
	}
}

func TestConfig_Defaults_FillsZeroLimitFields(t *testing.T) {
	cfg := Config{} // DefaultVcpuCount=0, DefaultMemoryMib=0
	cfg.defaults()
	if cfg.DefaultVcpuCount != 1 {
		t.Errorf("DefaultVcpuCount = %d, want 1", cfg.DefaultVcpuCount)
	}
	if cfg.DefaultMemoryMib != 256 {
		t.Errorf("DefaultMemoryMib = %d, want 256", cfg.DefaultMemoryMib)
	}
}

func TestConfig_Defaults_RespectsNonZeroLimitFields(t *testing.T) {
	cfg := Config{DefaultVcpuCount: 4, DefaultMemoryMib: 1024}
	cfg.defaults()
	if cfg.DefaultVcpuCount != 4 {
		t.Errorf("DefaultVcpuCount = %d, want 4 (preserved)", cfg.DefaultVcpuCount)
	}
	if cfg.DefaultMemoryMib != 1024 {
		t.Errorf("DefaultMemoryMib = %d, want 1024 (preserved)", cfg.DefaultMemoryMib)
	}
}
