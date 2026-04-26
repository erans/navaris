package main

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/storage"
)

func TestBuildStorageRegistry_AutoDefault(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: "auto",
	}
	reg, err := buildStorageRegistry(cfg)
	if err != nil {
		t.Fatalf("buildStorageRegistry: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	// On tmpfs the chosen backend is "copy".
	if got := reg.For(cfg.imageDir).Name(); got != "copy" {
		t.Errorf("imageDir backend = %q, want copy", got)
	}
}

func TestBuildStorageRegistry_ExplicitCopy(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: string(storage.ModeCopy),
	}
	if _, err := buildStorageRegistry(cfg); err != nil {
		t.Fatalf("buildStorageRegistry: %v", err)
	}
}

func TestBuildStorageRegistry_ExplicitReflink_OnTmpfsFails(t *testing.T) {
	cfg := config{
		chrootBase:  t.TempDir(),
		imageDir:    t.TempDir(),
		snapshotDir: t.TempDir(),
		storageMode: string(storage.ModeReflink),
	}
	_, err := buildStorageRegistry(cfg)
	if err == nil {
		t.Fatalf("expected explicit reflink on tmpfs to fail")
	}
	if !strings.Contains(err.Error(), "reflink") {
		t.Errorf("expected error mentioning reflink, got: %v", err)
	}
}

func TestBuildStorageRegistry_EmptyRootsAreSkipped(t *testing.T) {
	cfg := config{
		chrootBase:  "",
		imageDir:    "",
		snapshotDir: "",
		storageMode: "auto",
	}
	reg, err := buildStorageRegistry(cfg)
	if err != nil {
		t.Fatalf("buildStorageRegistry: %v", err)
	}
	// No roots → only the fallback is registered. For() falls back to copy.
	if got := reg.For("/anything").Name(); got != "copy" {
		t.Errorf("no-roots registry should fall back to copy, got %q", got)
	}
}

func TestBuildStorageRegistry_UnknownModeFails(t *testing.T) {
	cfg := config{
		imageDir:    t.TempDir(),
		storageMode: "garbage",
	}
	_, err := buildStorageRegistry(cfg)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestParseFlags_FirecrackerDefaults_Defaults(t *testing.T) {
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"navarisd"}
	cfg := parseFlags()
	if cfg.firecrackerDefaultVcpu != 1 {
		t.Errorf("default vcpu = %d, want 1", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 256 {
		t.Errorf("default memory mb = %d, want 256", cfg.firecrackerDefaultMemoryMB)
	}
}

func TestParseFlags_FirecrackerDefaults_Explicit(t *testing.T) {
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	os.Args = []string{"navarisd", "--firecracker-default-vcpu=4", "--firecracker-default-memory-mb=1024"}
	cfg := parseFlags()
	if cfg.firecrackerDefaultVcpu != 4 {
		t.Errorf("explicit vcpu = %d, want 4", cfg.firecrackerDefaultVcpu)
	}
	if cfg.firecrackerDefaultMemoryMB != 1024 {
		t.Errorf("explicit memory mb = %d, want 1024", cfg.firecrackerDefaultMemoryMB)
	}
}
