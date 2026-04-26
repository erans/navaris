package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// BoostService manages time-bounded resource boosts. See
// docs/superpowers/specs/2026-04-26-sandbox-boost-design.md.
type BoostService struct {
	boosts      domain.BoostStore
	sandboxes   domain.SandboxStore
	sandboxSvc  *SandboxService
	events      domain.EventBus
	clock       Clock
	maxDuration time.Duration

	mu     sync.Mutex
	timers map[string]Timer // keyed by boost_id
}

func NewBoostService(
	boosts domain.BoostStore,
	sandboxes domain.SandboxStore,
	sandboxSvc *SandboxService,
	events domain.EventBus,
	clock Clock,
	maxDuration time.Duration,
) *BoostService {
	return &BoostService{
		boosts:      boosts,
		sandboxes:   sandboxes,
		sandboxSvc:  sandboxSvc,
		events:      events,
		clock:       clock,
		maxDuration: maxDuration,
		timers:      make(map[string]Timer),
	}
}

// StartBoostOpts is the input to BoostService.Start.
type StartBoostOpts struct {
	SandboxID       string
	CPULimit        *int
	MemoryLimitMB   *int
	DurationSeconds int
}

// Get returns the active or revert_failed boost for a sandbox, or
// domain.ErrNotFound if none exists.
func (s *BoostService) Get(ctx context.Context, sandboxID string) (*domain.Boost, error) {
	return s.boosts.Get(ctx, sandboxID)
}

func (s *BoostService) Start(ctx context.Context, opts StartBoostOpts) (*domain.Boost, error) {
	if opts.DurationSeconds <= 0 {
		return nil, fmt.Errorf("duration_seconds must be > 0: %w", domain.ErrInvalidArgument)
	}
	dur := time.Duration(opts.DurationSeconds) * time.Second
	if dur > s.maxDuration {
		return nil, fmt.Errorf("duration_seconds %d exceeds max %d: %w",
			opts.DurationSeconds, int(s.maxDuration.Seconds()), domain.ErrInvalidArgument)
	}
	if opts.CPULimit == nil && opts.MemoryLimitMB == nil {
		return nil, fmt.Errorf("at least one of cpu_limit, memory_limit_mb must be supplied: %w",
			domain.ErrInvalidArgument)
	}

	sbx, err := s.sandboxes.Get(ctx, opts.SandboxID)
	if err != nil {
		return nil, err
	}
	if sbx.State != domain.SandboxRunning {
		return nil, fmt.Errorf("boost requires sandbox state running, got %s: %w",
			sbx.State, domain.ErrInvalidState)
	}
	if err := validateResourceBounds(opts.CPULimit, opts.MemoryLimitMB, sbx.Backend); err != nil {
		return nil, err
	}

	// Cancel any existing boost — replace semantics. Hold s.mu across the
	// timer cancel + row delete + new timer schedule to prevent two boosts
	// being in flight for the same sandbox.
	s.mu.Lock()
	defer s.mu.Unlock()

	if prior, err := s.boosts.Get(ctx, opts.SandboxID); err == nil {
		if t, ok := s.timers[prior.BoostID]; ok {
			t.Stop()
			delete(s.timers, prior.BoostID)
		}
		if err := s.boosts.Delete(ctx, prior.BoostID); err != nil {
			return nil, fmt.Errorf("delete prior boost: %w", err)
		}
	}

	now := s.clock.Now().UTC()
	boost := &domain.Boost{
		BoostID:               "bst-" + uuid.NewString()[:8],
		SandboxID:             sbx.SandboxID,
		OriginalCPULimit:      copyIntPtr(sbx.CPULimit),
		OriginalMemoryLimitMB: copyIntPtr(sbx.MemoryLimitMB),
		BoostedCPULimit:       copyIntPtr(opts.CPULimit),
		BoostedMemoryLimitMB:  copyIntPtr(opts.MemoryLimitMB),
		StartedAt:             now,
		ExpiresAt:             now.Add(dur),
		State:                 domain.BoostActive,
	}
	if err := s.boosts.Upsert(ctx, boost); err != nil {
		return nil, fmt.Errorf("persist boost: %w", err)
	}

	// Apply live-only — the persisted limits stay as the user's intent.
	_, err = s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      opts.CPULimit,
		MemoryLimitMB: opts.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if err != nil {
		// Roll back the boost row; the live VM is unchanged.
		if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil {
			return nil, fmt.Errorf("apply boost failed: %v; rollback also failed: %w", err, delErr)
		}
		return nil, err
	}

	// Schedule the auto-revert timer. The callback runs in a goroutine;
	// expire() takes the lock itself.
	s.timers[boost.BoostID] = s.clock.AfterFunc(dur, func() {
		s.expire(context.Background(), boost.BoostID)
	})

	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventBoostStarted,
		Timestamp: now,
		Data: map[string]any{
			"boost_id":                boost.BoostID,
			"sandbox_id":              boost.SandboxID,
			"boosted_cpu_limit":       boost.BoostedCPULimit,
			"boosted_memory_limit_mb": boost.BoostedMemoryLimitMB,
			"expires_at":              boost.ExpiresAt.Format(time.RFC3339Nano),
		},
	})

	return boost, nil
}

// expire is filled in Task 8. For now it deletes the row so timer-fire
// in tests doesn't dangle.
func (s *BoostService) expire(ctx context.Context, boostID string) {
	s.mu.Lock()
	delete(s.timers, boostID)
	s.mu.Unlock()
	_ = s.boosts.Delete(ctx, boostID)
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (s *BoostService) Cancel(ctx context.Context, sandboxID string) error {
	return errors.New("BoostService.Cancel: not implemented")
}

func (s *BoostService) Recover(ctx context.Context) error {
	return errors.New("BoostService.Recover: not implemented")
}

func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	// filled in Task 9
	_ = ctx
	_ = sandboxID
}
