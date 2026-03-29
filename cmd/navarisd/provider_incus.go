//go:build incus

package main

import (
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/incus"
)

func newIncusProvider(socket string) (domain.Provider, error) {
	return incus.New(incus.Config{
		Socket: socket,
	})
}
