//go:build firecracker

package firecracker

import (
	"encoding/json"
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
