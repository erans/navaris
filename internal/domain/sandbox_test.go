package domain_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestSandboxStateValid(t *testing.T) {
	valid := []domain.SandboxState{
		domain.SandboxPending,
		domain.SandboxStarting,
		domain.SandboxRunning,
		domain.SandboxStopping,
		domain.SandboxStopped,
		domain.SandboxFailed,
		domain.SandboxDestroyed,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("expected %q to be valid", s)
		}
	}
	if domain.SandboxState("bogus").Valid() {
		t.Error("expected bogus state to be invalid")
	}
}

func TestSandboxCanTransitionTo(t *testing.T) {
	tests := []struct {
		from, to domain.SandboxState
		ok       bool
	}{
		{domain.SandboxPending, domain.SandboxStarting, true},
		{domain.SandboxStarting, domain.SandboxRunning, true},
		{domain.SandboxRunning, domain.SandboxStopping, true},
		{domain.SandboxStopping, domain.SandboxStopped, true},
		{domain.SandboxStopped, domain.SandboxStarting, true},
		{domain.SandboxStopped, domain.SandboxDestroyed, true},
		{domain.SandboxStarting, domain.SandboxFailed, true},
		{domain.SandboxRunning, domain.SandboxFailed, true},
		{domain.SandboxStopping, domain.SandboxFailed, true},
		// Invalid transitions
		{domain.SandboxPending, domain.SandboxRunning, false},
		{domain.SandboxDestroyed, domain.SandboxRunning, false},
		{domain.SandboxRunning, domain.SandboxPending, false},
	}
	for _, tt := range tests {
		got := tt.from.CanTransitionTo(tt.to)
		if got != tt.ok {
			t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
		}
	}
}
