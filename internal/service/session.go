package service

import (
	"context"
	"fmt"
	"io"
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
	tmuxMu     sync.Map // per-sandbox *sync.Mutex for serializing ensureTmux
	bashReady  sync.Map
	bashMu     sync.Map // per-sandbox *sync.Mutex for serializing ensureBash
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

	// Ensure bash is available before setting up the session shell.
	if shell == "/bin/bash" {
		if err := s.ensureBash(ctx, sbx.SandboxID, ref); err != nil {
			// bash unavailable — fall back to /bin/sh.
			sess.Shell = "/bin/sh"
			sess.UpdatedAt = time.Now().UTC()
			_ = s.sessions.Update(ctx, sess)
		}
	}

	if sess.Backing == domain.SessionBackingTmux {
		if err := s.ensureTmux(ctx, sbx.SandboxID, ref); err != nil {
			// tmux unavailable — fall back to a direct (non-persistent) session.
			sess.Backing = domain.SessionBackingDirect
			sess.UpdatedAt = time.Now().UTC()
			_ = s.sessions.Update(ctx, sess)
			return sess, nil
		}
		tmuxCmd := []string{"tmux", "new-session", "-d", "-s", sess.SessionID}
		if err := s.execRun(ctx, ref, tmuxCmd); err != nil {
			// tmux session failed to start — fall back to direct.
			sess.Backing = domain.SessionBackingDirect
			sess.UpdatedAt = time.Now().UTC()
			_ = s.sessions.Update(ctx, sess)
			return sess, nil
		}
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
		err := fmt.Errorf("cannot detach session in state %s: %w", sess.State, domain.ErrInvalidState)
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

	// Serialize concurrent callers per sandbox so only one goroutine
	// runs the probe+install sequence while the others wait.
	muI, _ := s.tmuxMu.LoadOrStore(sandboxID, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Re-check after acquiring the lock — another goroutine may have
	// already completed the install while we were waiting.
	if _, ok := s.tmuxReady.Load(sandboxID); ok {
		return nil
	}

	// Probe for binaries using "test -x <path>" which is a coreutil,
	// not a shell builtin like "command -v" and not "which" (missing
	// on minimal Alpine images).
	testExec := func(path string) bool {
		return s.execCheck(ctx, ref, []string{"test", "-x", path})
	}
	if !testExec("/usr/bin/tmux") {
		var installCmd []string
		switch {
		case testExec("/sbin/apk"):
			installCmd = []string{"apk", "add", "--no-cache", "tmux"}
		case testExec("/usr/bin/apt-get"):
			installCmd = []string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends tmux"}
		default:
			return fmt.Errorf("tmux not available and no supported package manager found")
		}
		if err := s.execRun(ctx, ref, installCmd); err != nil {
			return fmt.Errorf("install tmux: %w", err)
		}
	}

	// Write a tmux.conf so the settings apply from server start (before
	// any windows are created). Skip if the rootfs already has one baked in.
	if !s.execCheck(ctx, ref, []string{"test", "-f", "/root/.tmux.conf"}) {
		hasBash := testExec("/bin/bash")
		tmuxConf := `set -g default-terminal "xterm-256color"
set -g mouse on
set -s set-clipboard on`
		if hasBash {
			tmuxConf += "\nset -g default-shell /bin/bash\nset -g default-command \"/bin/bash -l\""
		}
		_ = s.execRun(ctx, ref, []string{"sh", "-c",
			"cat > /root/.tmux.conf << 'TMUXEOF'\n" + tmuxConf + "\nTMUXEOF"})
	}

	s.tmuxReady.Store(sandboxID, true)
	return nil
}

func (s *SessionService) ensureBash(ctx context.Context, sandboxID string, ref domain.BackendRef) error {
	if _, ok := s.bashReady.Load(sandboxID); ok {
		return nil
	}

	muI, _ := s.bashMu.LoadOrStore(sandboxID, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	if _, ok := s.bashReady.Load(sandboxID); ok {
		return nil
	}

	testExec := func(path string) bool {
		return s.execCheck(ctx, ref, []string{"test", "-x", path})
	}
	isAlpine := testExec("/sbin/apk")

	if !testExec("/bin/bash") {
		var installCmd []string
		switch {
		case isAlpine:
			installCmd = []string{"apk", "add", "--no-cache", "bash"}
		case testExec("/usr/bin/apt-get"):
			installCmd = []string{"sh", "-c", "apt-get update && apt-get install -y --no-install-recommends bash"}
		default:
			return fmt.Errorf("bash not available and no supported package manager found")
		}
		if err := s.execRun(ctx, ref, installCmd); err != nil {
			return fmt.Errorf("install bash: %w", err)
		}
	}

	// Alpine has no default color setup for bash. Write color config files
	// only if they don't already exist (rootfs images may have them baked in;
	// overwriting via heredoc-through-exec can truncate them).
	if isAlpine && !s.execCheck(ctx, ref, []string{"test", "-f", "/root/.bashrc"}) {
		_ = s.execRun(ctx, ref, []string{"sh", "-c", `mkdir -p /etc/profile.d && cat > /etc/profile.d/colors.sh << 'COLORSEOF'
export LS_OPTIONS='--color=auto'
alias ls='ls $LS_OPTIONS'
alias grep='grep --color=auto'
if [ "$(id -u)" -eq 0 ]; then
  PS1='\[\e[1;31m\]\h\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]# '
else
  PS1='\[\e[1;32m\]\u@\h\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]\$ '
fi
COLORSEOF`})
		_ = s.execRun(ctx, ref, []string{"sh", "-c", `cat > /root/.bashrc << 'COLORSEOF'
export LS_OPTIONS='--color=auto'
alias ls='ls $LS_OPTIONS'
alias grep='grep --color=auto'
if [ "$(id -u)" -eq 0 ]; then
  PS1='\[\e[1;31m\]\h\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]# '
else
  PS1='\[\e[1;32m\]\u@\h\[\e[0m\]:\[\e[1;34m\]\w\[\e[0m\]\$ '
fi
COLORSEOF`})
	}

	s.bashReady.Store(sandboxID, true)
	return nil
}

func (s *SessionService) execCheck(ctx context.Context, ref domain.BackendRef, cmd []string) bool {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return false
	}
	// Drain pipes so the process doesn't block on output.
	go io.Copy(io.Discard, handle.Stdout)
	go io.Copy(io.Discard, handle.Stderr)
	code, err := handle.Wait()
	handle.Stdout.Close()
	handle.Stderr.Close()
	return err == nil && code == 0
}

func (s *SessionService) execRun(ctx context.Context, ref domain.BackendRef, cmd []string) error {
	handle, err := s.provider.Exec(ctx, ref, domain.ExecRequest{Command: cmd})
	if err != nil {
		return err
	}
	// Drain pipes so the process doesn't block on output.
	go io.Copy(io.Discard, handle.Stdout)
	go io.Copy(io.Discard, handle.Stderr)
	code, err := handle.Wait()
	handle.Stdout.Close()
	handle.Stderr.Close()
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
	s.bashReady.Delete(sandboxID)
}
