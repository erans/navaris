package domain_test

import (
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

func TestSnapshotStateTransitions(t *testing.T) {
	tests := []struct {
		from, to domain.SnapshotState
		ok       bool
	}{
		{domain.SnapshotPending, domain.SnapshotCreating, true},
		{domain.SnapshotCreating, domain.SnapshotReady, true},
		{domain.SnapshotCreating, domain.SnapshotFailed, true},
		{domain.SnapshotReady, domain.SnapshotDeleted, true},
		{domain.SnapshotPending, domain.SnapshotReady, false},
	}
	for _, tt := range tests {
		if got := tt.from.CanTransitionTo(tt.to); got != tt.ok {
			t.Errorf("%s → %s: got %v, want %v", tt.from, tt.to, got, tt.ok)
		}
	}
}
