package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestErrorWrapping(t *testing.T) {
	wrapped := fmt.Errorf("sandbox abc: %w", domain.ErrNotFound)
	if !errors.Is(wrapped, domain.ErrNotFound) {
		t.Fatal("expected wrapped error to match ErrNotFound")
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if errors.Is(domain.ErrNotFound, domain.ErrConflict) {
		t.Fatal("ErrNotFound should not match ErrConflict")
	}
}
