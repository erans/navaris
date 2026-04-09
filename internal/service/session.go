package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type SessionService struct {
	sessions   domain.SessionStore
	sandboxes  domain.SandboxStore
	provider   domain.Provider
	events     domain.EventBus
	tmuxReady  sync.Map
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
		backing = domain.SessionBackingTmux
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

	ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
	if err := s.ensureTmux(ctx, sbx.SandboxID, ref); err != nil {
		_ = s.sessions.Delete(ctx, sess.SessionID)
		return nil, fmt.Errorf("ensure tmux: %w", err)
	}
	tmuxCmd := []string{"tmux", "new-session", "-d", "-s", sess.SessionID, shell}
	if err := s.execRun(ctx, ref, tmuxCmd); err != nil {
		_ = s.sessions.Delete(ctx, sess.SessionID)
		return nil, fmt.Errorf("tmux new-session: %w", err)
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
	if !sess.State.CanTransitionTo(domain.SessionDestroyed) {
		return fmt.Errorf("cannot destroy session in state %s: %w", sess.State, domain.ErrInvalidState)
	}
	if sbx, err := s.sandboxes.Get(ctx, sess.SandboxID); err == nil && sbx.State == domain.SandboxRunning {
		ref := domain.BackendRef{Backend: sbx.Backend, Ref: sbx.BackendRef}
		_ = s.execRun(ctx, ref, []string{"tmux", "kill-session", "-t", sess.SessionID})
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

func (s *SessionService) ensureTmux(ctx context.Context, sandboxID string, ref domain.BackendRef) error {
	if _, ok := s.tmuxReady.Load(sandboxID); ok {
		return nil
	}
	if s.execCheck(ctx, ref, []string{"command", "-v", "tmux"}) {
		s.tmuxReady.Store(sandboxID, true)
		return nil
	}
	var installCmd []string
	switch {
	case s.execCheck(ctx, ref, []string{"command", "-v", "apk"}):
		installCmd = []string{"apk", "add", "--no-cache", "tmux"}
	case s.execCheck(ctx, ref, []string{"command", "-v", "apt-get"}):
		installCmd = []string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends tmux"}
	default:
		return fmt.Errorf("tmux not available and no supported package manager found")
	}
	if err := s.execRun(ctx, ref, installCmd); err != nil {
		return fmt.Errorf("install tmux: %w", err)
	}
	s.tmuxReady.Store(sandboxID, true)
	return nil
}

func (s *SessionService) execCheck(ctx context.Context, ref domain.BackendRef, cmd []string) bool {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return false
	}
	defer handle.Stdout.Close()
	defer handle.Stderr.Close()
	code, err := handle.Wait()
	return err == nil && code == 0
}

func (s *SessionService) execRun(ctx context.Context, ref domain.BackendRef, cmd []string) error {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return err
	}
	defer handle.Stdout.Close()
	defer handle.Stderr.Close()
	code, err := handle.Wait()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("command %v exited with code %d", cmd, code)
	}
	return nil
}

func (s *SessionService) ClearTmuxCache(sandboxID string) {
	s.tmuxReady.Delete(sandboxID)
}
