//go:build !incus

package main

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

func newIncusProvider(_ string) (domain.Provider, error) {
	return nil, fmt.Errorf("incus provider not available: binary built without 'incus' build tag")
}
