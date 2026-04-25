//go:build firecracker

package main

import (
	"os"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker"
	"github.com/navaris/navaris/internal/storage"
)

func newFirecrackerProvider(cfg config) (domain.Provider, error) {
	// Minimal storage registry: copy-only fallback. Task 9 replaces this
	// with a probe-built registry honoring --storage-mode.
	reg := storage.NewRegistry()
	reg.SetFallback(storage.CopyBackend{})

	return firecracker.New(firecracker.Config{
		FirecrackerBin: cfg.firecrackerBin,
		JailerBin:      cfg.jailerBin,
		KernelPath:     cfg.kernelPath,
		ImageDir:       cfg.imageDir,
		ChrootBase:     cfg.chrootBase,
		HostInterface:  cfg.hostInterface,
		SnapshotDir:    cfg.snapshotDir,
		EnableJailer:   cfg.enableJailer,
		Storage:        reg,
	})
}

func kvmAvailable() bool {
	f, err := os.Open("/dev/kvm")
	if err != nil {
		return false
	}
	f.Close()
	return true
}
