//go:build incus

package main

import (
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/incus"
)

func newIncusProvider(cfg config) (domain.Provider, error) {
	return incus.New(incus.Config{
		Socket:        cfg.incusSocket,
		StrictPoolCoW: cfg.incusStrictPoolCoW,
	})
}
