package domain

import "time"

type SessionState string

const (
	SessionActive    SessionState = "active"
	SessionDetached  SessionState = "detached"
	SessionExited    SessionState = "exited"
	SessionDestroyed SessionState = "destroyed"
)

func (s SessionState) Valid() bool {
	switch s {
	case SessionActive, SessionDetached, SessionExited, SessionDestroyed:
		return true
	}
	return false
}

var sessionTransitions = map[SessionState][]SessionState{
	SessionActive:   {SessionDetached, SessionExited, SessionDestroyed},
	SessionDetached: {SessionActive, SessionExited, SessionDestroyed},
	SessionExited:   {SessionDestroyed},
}

func (s SessionState) CanTransitionTo(target SessionState) bool {
	for _, allowed := range sessionTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

type SessionBacking string

const (
	SessionBackingDirect SessionBacking = "direct"
	SessionBackingTmux   SessionBacking = "tmux"
	SessionBackingAuto   SessionBacking = "auto"
)

type Session struct {
	SessionID      string
	SandboxID      string
	Backing        SessionBacking
	Shell          string
	State          SessionState
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAttachedAt *time.Time
	IdleTimeout    *time.Duration
	Metadata       map[string]any
}
