//go:build firecracker

package main

import (
	"os"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker"
)

func newFirecrackerProvider(cfg config) (domain.Provider, error) {
	return firecracker.New(firecracker.Config{
		FirecrackerBin: cfg.firecrackerBin,
		JailerBin:      cfg.jailerBin,
		KernelPath:     cfg.kernelPath,
		ImageDir:       cfg.imageDir,
		ChrootBase:     cfg.chrootBase,
		HostInterface:  cfg.hostInterface,
		SnapshotDir:    cfg.snapshotDir,
		EnableJailer:   cfg.enableJailer,
		Storage:        cfg.storageRegistry,
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
