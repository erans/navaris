package domain_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestSessionStateTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.SessionState
		ok       bool
	}{
		{domain.SessionActive, domain.SessionDetached, true},
		{domain.SessionDetached, domain.SessionActive, true},
		{domain.SessionActive, domain.SessionExited, true},
		{domain.SessionDetached, domain.SessionExited, true},
		{domain.SessionExited, domain.SessionDestroyed, true},
		{domain.SessionExited, domain.SessionActive, false},
	}
	for _, tt := range tests {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.ok {
			t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
		}
	}
}
