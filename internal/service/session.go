package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

type SessionService struct {
	sessions  domain.SessionStore
	sandboxes domain.SandboxStore
	provider  domain.Provider
	events    domain.EventBus
}

func NewSessionService(
	sessions domain.SessionStore,
	sandboxes domain.SandboxStore,
	provider domain.Provider,
	events domain.EventBus,
) *SessionService {
	return &SessionService{
		sessions: sessions, sandboxes: sandboxes,
		provider: provider, events: events,
	}
}

func (s *SessionService) Create(ctx context.Context, sandboxID string, backing domain.SessionBacking, shell string) (*domain.Session, error) {
	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if sbx.State != domain.SandboxRunning {
		return nil, fmt.Errorf("sandbox must be running to create session (state: %s): %w", sbx.State, domain.ErrInvalidState)
	}

	if shell == "" {
		shell = "/bin/bash"
	}
	if backing == "" || backing == domain.SessionBackingAuto {
		backing = domain.SessionBackingDirect
	}

	now := time.Now().UTC()
	sess := &domain.Session{
		SessionID: uuid.NewString(),
		SandboxID: sandboxID,
		Backing:   backing,
		Shell:     shell,
		State:     domain.SessionActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *SessionService) Get(ctx context.Context, id string) (*domain.Session, error) {
	return s.sessions.Get(ctx, id)
}

func (s *SessionService) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.Session, error) {
	return s.sessions.ListBySandbox(ctx, sandboxID)
}

func (s *SessionService) Destroy(ctx context.Context, id string) error {
	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		return err
	}
	sess.State = domain.SessionDestroyed
	sess.UpdatedAt = time.Now().UTC()
	return s.sessions.Update(ctx, sess)
}
