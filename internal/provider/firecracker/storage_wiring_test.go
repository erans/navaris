//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
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

func TestResolveMachineLimits(t *testing.T) {
	p := &Provider{config: Config{DefaultVcpuCount: 1, DefaultMemoryMib: 256, VcpuHeadroomMult: 1.0, MemHeadroomMult: 1.0}}

	cases := []struct {
		name     string
		req      domain.CreateSandboxRequest
		wantVcpu int64
		wantMem  int64
	}{
		{name: "all nil → defaults", req: domain.CreateSandboxRequest{}, wantVcpu: 1, wantMem: 256},
		{name: "cpu set", req: domain.CreateSandboxRequest{CPULimit: ptrInt(4)}, wantVcpu: 4, wantMem: 256},
		{name: "mem set", req: domain.CreateSandboxRequest{MemoryLimitMB: ptrInt(512)}, wantVcpu: 1, wantMem: 512},
		{name: "both set", req: domain.CreateSandboxRequest{CPULimit: ptrInt(2), MemoryLimitMB: ptrInt(1024)}, wantVcpu: 2, wantMem: 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.resolveMachineLimits(tc.req)
			if got.LimitCPU != tc.wantVcpu {
				t.Errorf("vcpu = %d, want %d", got.LimitCPU, tc.wantVcpu)
			}
			if got.LimitMemMib != tc.wantMem {
				t.Errorf("mem = %d, want %d", got.LimitMemMib, tc.wantMem)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
