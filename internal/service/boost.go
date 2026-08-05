package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

// BoostService manages time-bounded resource boosts. See
// docs/superpowers/specs/2026-04-26-sandbox-boost-design.md.
type sandboxLock struct {
	mu   sync.Mutex
	refs int // guarded by BoostService.mu; holders and waiters both count
}

type BoostService struct {
	boosts      domain.BoostStore
	sandboxes   domain.SandboxStore
	sandboxSvc  *SandboxService
	events      domain.EventBus
	clock       Clock
	maxDuration time.Duration

	mu       sync.Mutex
	timers   map[string]Timer // keyed by boost_id
	sbxLocks map[string]*sandboxLock
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
		sbxLocks:    make(map[string]*sandboxLock),
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

	releaseSandbox := s.acquireSandbox(opts.SandboxID)
	defer releaseSandbox()

	sbx, err = s.sandboxes.Get(ctx, opts.SandboxID)
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

	prior, getErr := s.boosts.Get(ctx, opts.SandboxID)
	if getErr != nil {
		if !errors.Is(getErr, domain.ErrNotFound) {
			return nil, fmt.Errorf("get prior boost: %w", getErr)
		}
		prior = nil
	}
	if prior != nil {
		s.stopTimer(prior.BoostID)
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
		if prior != nil && prior.State == domain.BoostActive {
			s.armRemaining(prior)
		}
		return nil, fmt.Errorf("persist boost: %w", err)
	}

	_, err = s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      opts.CPULimit,
		MemoryLimitMB: opts.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if err != nil {
		applyErr := fmt.Errorf("apply boost: %w", err)
		var rollbackErr error
		if prior != nil {
			if restoreErr := s.boosts.Upsert(ctx, prior); restoreErr != nil {
				rollbackErr = fmt.Errorf("restore prior boost: %w", restoreErr)
				s.armRetry(boost.BoostID)
			} else if prior.State == domain.BoostActive {
				s.armRemaining(prior)
			}
		} else {
			if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil {
				if !errors.Is(delErr, domain.ErrNotFound) {
					rollbackErr = fmt.Errorf("delete failed boost: %w", delErr)
					s.armRetry(boost.BoostID)
				}
			}
		}
		if rollbackErr != nil {
			return nil, errors.Join(applyErr, rollbackErr)
		}
		return nil, applyErr
	}

	s.armRemaining(boost)

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

// acquireSandbox returns with the sandbox's operation lock held. The returned
// release function must be called exactly once. Registration increments refs
// before waiting, so the entry cannot disappear while a holder or waiter
// retains its pointer.
func (s *BoostService) acquireSandbox(sandboxID string) func() {
	s.mu.Lock()
	entry, ok := s.sbxLocks[sandboxID]
	if !ok {
		entry = &sandboxLock{}
		s.sbxLocks[sandboxID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.sbxLocks[sandboxID] == entry {
			delete(s.sbxLocks, sandboxID)
		}
		s.mu.Unlock()
	}
}

func (s *BoostService) armExpiry(boostID string, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	s.mu.Lock()
	if timer, ok := s.timers[boostID]; ok {
		timer.Stop()
	}
	s.timers[boostID] = s.clock.AfterFunc(delay, func() {
		s.expire(context.Background(), boostID)
	})
	s.mu.Unlock()
}

func (s *BoostService) armRemaining(b *domain.Boost) {
	s.armExpiry(b.BoostID, b.ExpiresAt.Sub(s.clock.Now().UTC()))
}

func (s *BoostService) armRetry(boostID string) {
	s.armExpiry(boostID, boostBackoff[0])
}

func (s *BoostService) stopTimer(boostID string) {
	s.mu.Lock()
	if timer, ok := s.timers[boostID]; ok {
		timer.Stop()
		delete(s.timers, boostID)
	}
	s.mu.Unlock()
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
	candidate, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.stopTimer(boostID)
			return
		}
		s.armRetry(boostID)
		slog.Warn("boost: transient boost lookup failed during expiry", "boost_id", boostID, "error", err)
		return
	}

	releaseSandbox := s.acquireSandbox(candidate.SandboxID)
	defer releaseSandbox()

	boost, err := s.boosts.GetByID(ctx, boostID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.stopTimer(boostID)
			return
		}
		s.armRetry(boostID)
		slog.Warn("boost: transient boost re-read failed during expiry", "boost_id", boostID, "error", err)
		return
	}
	s.stopTimer(boostID)

	sbx, err := s.sandboxes.Get(ctx, boost.SandboxID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if delErr := s.boosts.Delete(ctx, boostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
				s.armRetry(boostID)
				slog.Warn("boost: delete after missing sandbox failed", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", delErr)
				return
			}
			return
		}
		s.armRetry(boostID)
		slog.Warn("boost: transient sandbox lookup failed during expiry", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", err)
		return
	}
	if sbx.State != domain.SandboxRunning {
		if delErr := s.boosts.Delete(ctx, boostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
			s.armRetry(boostID)
			slog.Warn("boost: delete for non-running sandbox failed", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", delErr)
			return
		}
		s.emitExpired(ctx, boost, "sandbox_not_running", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	_, applyErr := s.sandboxSvc.UpdateResources(ctx, UpdateResourcesOpts{
		SandboxID:     sbx.SandboxID,
		CPULimit:      sbx.CPULimit,
		MemoryLimitMB: sbx.MemoryLimitMB,
		ApplyLiveOnly: true,
	})
	if applyErr == nil {
		if delErr := s.boosts.Delete(ctx, boostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
			s.armRetry(boostID)
			slog.Warn("boost: delete after successful revert failed", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", delErr)
			return
		}
		s.emitExpired(ctx, boost, "expired", sbx.CPULimit, sbx.MemoryLimitMB)
		return
	}

	attempts := boost.RevertAttempts + 1
	if attempts > len(boostBackoff) {
		if err := s.boosts.UpdateState(ctx, boostID, domain.BoostRevertFailed, attempts, applyErr.Error()); err != nil {
			if !errors.Is(err, domain.ErrNotFound) {
				s.armRetry(boostID)
				slog.Warn("boost: mark revert_failed failed", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", err)
			}
			return
		}
		_ = s.events.Publish(ctx, domain.Event{
			Type:      domain.EventBoostRevertFailed,
			Timestamp: s.clock.Now().UTC(),
			Data: map[string]any{
				"boost_id": boostID, "sandbox_id": boost.SandboxID,
				"attempts": attempts, "last_error": applyErr.Error(),
				"source": "external",
			},
		})
		return
	}

	if err := s.boosts.UpdateState(ctx, boostID, domain.BoostActive, attempts, applyErr.Error()); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			s.armRetry(boostID)
			slog.Warn("boost: update retry state failed", "boost_id", boostID, "sandbox_id", boost.SandboxID, "error", err)
		}
		return
	}
	s.armExpiry(boostID, boostBackoff[attempts-1])
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
	releaseSandbox := s.acquireSandbox(sandboxID)
	defer releaseSandbox()

	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		return err
	}
	s.stopTimer(boost.BoostID)

	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
				s.armRetry(boost.BoostID)
				return errors.Join(err, fmt.Errorf("delete boost after missing sandbox: %w", delErr))
			}
			return err
		}
		if boost.State == domain.BoostActive {
			s.armRemaining(boost)
		}
		return err
	}
	if sbx.State != domain.SandboxRunning {
		if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
			s.armRetry(boost.BoostID)
			return fmt.Errorf("delete boost after cancel: %w", delErr)
		}
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
		stateErr := s.boosts.UpdateState(ctx, boost.BoostID, domain.BoostRevertFailed,
			boost.RevertAttempts+1, applyErr.Error())
		if stateErr != nil {
			if !errors.Is(stateErr, domain.ErrNotFound) {
				s.armRetry(boost.BoostID)
				return errors.Join(applyErr, fmt.Errorf("update boost state: %w", stateErr))
			}
		}
		return applyErr
	}

	if delErr := s.boosts.Delete(ctx, boost.BoostID); delErr != nil && !errors.Is(delErr, domain.ErrNotFound) {
		s.armRetry(boost.BoostID)
		return fmt.Errorf("delete boost after cancel: %w", delErr)
	}
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
		s.armRemaining(b)
	}
	return nil
}

// cancelOnLifecycle is called from SandboxService.Stop/Destroy. It drops
// the boost row + timer WITHOUT attempting a revert (the live VM is going
// away or being suspended; nothing to apply to). Errors are best-effort
// and are not propagated.
func (s *BoostService) cancelOnLifecycle(ctx context.Context, sandboxID string) {
	releaseSandbox := s.acquireSandbox(sandboxID)
	defer releaseSandbox()

	s.cancelOnLifecycleLocked(ctx, sandboxID)
}

// cancelOnLifecycleLocked is called while the caller holds the sandbox's
// operation lock. It drops the boost row + timer without attempting a revert.
func (s *BoostService) cancelOnLifecycleLocked(ctx context.Context, sandboxID string) {
	boost, err := s.boosts.Get(ctx, sandboxID)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("boost: lifecycle boost lookup failed", "sandbox_id", sandboxID, "error", err)
		}
		return
	}
	if err := s.boosts.Delete(ctx, boost.BoostID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.stopTimer(boost.BoostID)
			return
		}
		slog.Warn("boost: lifecycle boost delete failed", "boost_id", boost.BoostID, "sandbox_id", sandboxID, "error", err)
		return
	}
	s.stopTimer(boost.BoostID)
}
