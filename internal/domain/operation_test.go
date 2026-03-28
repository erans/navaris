package domain_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestOperationStateTerminal(t *testing.T) {
	terminal := []domain.OperationState{domain.OpSucceeded, domain.OpFailed, domain.OpCancelled}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	nonTerminal := []domain.OperationState{domain.OpPending, domain.OpRunning}
	for _, s := range nonTerminal {
		if s.Terminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}
