package domain

import (
	"context"
	"time"
)

type BoostState string

const (
	BoostActive       BoostState = "active"
	BoostRevertFailed BoostState = "revert_failed"
)

func (s BoostState) Valid() bool {
	switch s {
	case BoostActive, BoostRevertFailed:
		return true
	}
	return false
}

// Boost is a time-bounded resource bump applied to a running sandbox.
// The boosted limits are live-only — they are NOT written to the
// sandbox's persisted limits. At ExpiresAt (or on explicit cancel) the
// daemon applies the current persisted limits live again, restoring
// the user's intended steady-state.
type Boost struct {
	BoostID               string
	SandboxID             string
	OriginalCPULimit      *int // captured at boost time, for caller display
	OriginalMemoryLimitMB *int // captured at boost time, for caller display
	BoostedCPULimit       *int // nil if the boost only touched memory
	BoostedMemoryLimitMB  *int // nil if the boost only touched cpu
	StartedAt             time.Time
	ExpiresAt             time.Time
	State                 BoostState
	RevertAttempts        int
	LastError             string
}

// BoostStore persists Boost rows. UNIQUE(sandbox_id) is enforced at the
// schema level; Upsert replaces any existing boost for the same sandbox.
type BoostStore interface {
	Get(ctx context.Context, sandboxID string) (*Boost, error) // ErrNotFound if absent
	GetByID(ctx context.Context, boostID string) (*Boost, error)
	Upsert(ctx context.Context, b *Boost) error
	UpdateState(ctx context.Context, boostID string, state BoostState, attempts int, lastErr string) error
	Delete(ctx context.Context, boostID string) error
	ListAll(ctx context.Context) ([]*Boost, error)
}
