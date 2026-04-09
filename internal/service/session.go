package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateSession")
	defer span.End()

	span.SetAttributes(attribute.String("sandbox.id", sandboxID))

	sbx, err := s.sandboxes.Get(ctx, sandboxID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if sbx.State != domain.SandboxRunning {
		err := fmt.Errorf("sandbox must be running to create session (state: %s): %w", sbx.State, domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return sess, nil
}

func (s *SessionService) Get(ctx context.Context, id string) (*domain.Session, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetSession")
	defer span.End()

	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return sess, nil
}

func (s *SessionService) ListBySandbox(ctx context.Context, sandboxID string) ([]*domain.Session, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ListSessionsBySandbox")
	defer span.End()

	span.SetAttributes(attribute.String("sandbox.id", sandboxID))
	list, err := s.sessions.ListBySandbox(ctx, sandboxID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return list, nil
}

func (s *SessionService) Destroy(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DestroySession")
	defer span.End()

	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	sess.State = domain.SessionDestroyed
	sess.UpdatedAt = time.Now().UTC()
	if err := s.sessions.Update(ctx, sess); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (s *SessionService) Detach(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DetachSession")
	defer span.End()

	sess, err := s.sessions.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if !sess.State.CanTransitionTo(domain.SessionDetached) {
		err := domain.ErrInvalidState
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	now := time.Now().UTC()
	sess.State = domain.SessionDetached
	sess.UpdatedAt = now
	sess.LastAttachedAt = &now
	if err := s.sessions.Update(ctx, sess); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (s *SessionService) ExitAllForSandbox(ctx context.Context, sandboxID string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ExitAllSessionsForSandbox")
	defer span.End()

	span.SetAttributes(attribute.String("sandbox.id", sandboxID))

	list, err := s.sessions.ListBySandbox(ctx, sandboxID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	for _, sess := range list {
		if !sess.State.CanTransitionTo(domain.SessionExited) {
			continue
		}
		sess.State = domain.SessionExited
		sess.UpdatedAt = time.Now().UTC()
		if err := s.sessions.Update(ctx, sess); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
	}
	return nil
}
