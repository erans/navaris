//go:build !firecracker

package main

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

func newFirecrackerProvider(_ config) (domain.Provider, error) {
	return nil, fmt.Errorf("firecracker provider not available: binary built without 'firecracker' build tag")
}
