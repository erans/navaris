//go:build firecracker

package main

import (
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
	})
}
