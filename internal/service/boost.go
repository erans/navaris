package service

import (
	"context"
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
	Source          string // "external" (operator API) or "in_sandbox" (boost channel); empty defaults to "external"
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
	source := opts.Source
	if source == "" {
		source = "external"
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
		Source:                source,
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
			"source":                  source,
		},
	})

	return boost, nil
}

// boostBackoff is the per-attempt sleep between revert retries. The slice
// length is the maximum number of attempts. If a provider error persists
// past the last entry, the boost transitions to BoostRevertFailed.
var boostBackoff = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
}

func (s *BoostService) expire(ctx context.Context, boostID string) {
	s.mu.Lock()
	delete(s.timers, boostID)
	s.mu.Unlock()

	boost, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		// Race: boost was cancelled or deleted while the timer was firing.
		return
	}

	sbx, err := s.sandboxes.Get(ctx, boost.SandboxID)
	if err != nil {
		// Sandbox is gone; clean up the boost row.
		_ = s.boosts.Delete(ctx, boostID)
		return
	}
	if sbx.State != domain.SandboxRunning {
		// Defense-in-depth: lifecycle hooks should have removed this.
		_ = s.boosts.Delete(ctx, boostID)
		s.emitExpired(ctx, boost, "sandbox_not_running", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	// Apply the persisted (current) limits live.
	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		ApplyLiveOnly: true,
	})

	if applyErr == nil {
		_ = s.boosts.Delete(ctx, boostID)
		s.emitExpired(ctx, boost, "expired", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	// Failure: increment attempts, retry with backoff or transition to revert_failed.
	attempts := boost.RevertAttempts + 1
	if attempts > len(boostBackoff) {
		_ = s.boosts.UpdateState(ctx, boostID, domain.BoostRevertFailed, attempts, applyErr.Error())
		_ = s.events.Publish(ctx, domain.Event{
			Type:      domain.EventBoostRevertFailed,
			Timestamp: s.clock.Now().UTC(),
			Data: map[string]any{
				"boost_id":   boostID,
				"sandbox_id": boost.SandboxID,
				"attempts":   attempts,
				"last_error": applyErr.Error(),
				"source":     "external",
			},
		})
		return
	}

	_ = s.boosts.UpdateState(ctx, boostID, domain.BoostActive, attempts, applyErr.Error())

	// Schedule retry under the lock to keep the timers map consistent.
	s.mu.Lock()
	s.timers[boostID] = s.clock.AfterFunc(boostBackoff[attempts-1], func() {
		s.expire(context.Background(), boostID)
	})
	s.mu.Unlock()
}

func (s *BoostService) emitExpired(ctx context.Context, b *domain.Boost, cause string, cpu, mem *int) {
	_ = s.events.Publish(ctx, domain.Event{
		Type:      domain.EventBoostExpired,
		Timestamp: s.clock.Now().UTC(),
		Data: map[string]any{
			"boost_id":                 b.BoostID,
			"sandbox_id":               b.SandboxID,
			"cause":                    cause,
			"reverted_cpu_limit":       cpu,
			"reverted_memory_limit_mb": mem,
			"source":                   "external",
		},
	})
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Cancel reverts the active boost immediately and deletes the row. If no
// boost exists, returns ErrNotFound. If the boost is in BoostRevertFailed
// state, the cancel attempts the revert one more time and surfaces the
// provider error if it still fails.
func (s *BoostService) Cancel(ctx context.Context, sandboxID string) error {
	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if t, ok := s.timers[boost.BoostID]; ok {
		t.Stop()
		delete(s.timers, boost.BoostID)
	}
	s.mu.Unlock()

	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		_ = s.boosts.Delete(ctx, boost.BoostID)
		return err
	}
	if sbx.State != domain.SandboxRunning {
		_ = s.boosts.Delete(ctx, boost.BoostID)
		s.emitExpired(ctx, boost, "cancelled", sbx.CPULimit, sbx.MemoryLimitMB)
		return nil
	}

	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if applyErr != nil {
		// Surface to the caller; leave the row in revert_failed for visibility.
		_ = s.boosts.UpdateState(ctx, boost.BoostID, domain.BoostRevertFailed,
			boost.RevertAttempts+1, applyErr.Error())
		return applyErr
	}

	_ = s.boosts.Delete(ctx, boost.BoostID)
	s.emitExpired(ctx, boost, "cancelled", sbx.CPULimit, sbx.MemoryLimitMB)
	return nil
}

// Recover replays in-flight boosts after a daemon restart. For each
// BoostActive row: if it's already expired (e.g. the daemon was down past
// its ExpiresAt), trigger an immediate revert; otherwise schedule a timer
// for the remaining duration. BoostRevertFailed rows are left alone — they
// surface via GET and require operator action (DELETE) to clear.
//
// Recover should be called once at daemon startup, before the HTTP listener
// starts, so timers are armed before requests can arrive.
func (s *BoostService) Recover(ctx context.Context) error {
	rows, err := s.boosts.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list boosts: %w", err)
	}
	now := s.clock.Now().UTC()
	for _, b := range rows {
		if b.State != domain.BoostActive {
			// Leave revert_failed (and any future states) alone.
			continue
		}
		boostID := b.BoostID
		if !now.Before(b.ExpiresAt) {
			// Already expired; trigger revert immediately. Run in a fresh
			// goroutine so a slow provider doesn't block daemon startup.
			go s.expire(context.Background(), boostID)
			continue
		}
		remaining := b.ExpiresAt.Sub(now)
		s.mu.Lock()
		s.timers[boostID] = s.clock.AfterFunc(remaining, func() {
			s.expire(context.Background(), boostID)
		})
		s.mu.Unlock()
	}
	return nil
}

// cancelOnLifecycle is called from SandboxService.Stop/Destroy. It drops
// the boost row + timer WITHOUT attempting a revert (the live VM is going
// away or being suspended; nothing to apply to). Errors are best-effort
// and are not propagated.
func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return
	}
	s.mu.Lock()
	if t, ok := s.timers[boost.BoostID]; ok {
		t.Stop()
		delete(s.timers, boost.BoostID)
	}
	s.mu.Unlock()
	_ = s.boosts.Delete(ctx, boost.BoostID)
}
