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

// Start, Cancel, expire, cancelOnLifecycle, Recover are filled in by
// later tasks (6–10). The stubs here let the service type satisfy
// callers during bring-up.
func (s *BoostService) Start(ctx context.Context, opts StartBoostOpts) (*domain.Boost, error) {
	return nil, errors.New("BoostService.Start: not implemented")
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

// suppress unused-import noise during bring-up — these imports are used
// once the later tasks fill in the real method bodies.
var _ = fmt.Errorf
var _ = uuid.NewString
