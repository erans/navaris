package domain

import "time"

type SandboxState string

const (
	SandboxPending   SandboxState = "pending"
	SandboxStarting  SandboxState = "starting"
	SandboxRunning   SandboxState = "running"
	SandboxStopping  SandboxState = "stopping"
	SandboxStopped   SandboxState = "stopped"
	SandboxFailed    SandboxState = "failed"
	SandboxDestroyed SandboxState = "destroyed"
)

func (s SandboxState) Valid() bool {
	switch s {
	case SandboxPending, SandboxStarting, SandboxRunning,
		SandboxStopping, SandboxStopped, SandboxFailed, SandboxDestroyed:
		return true
	}
	return false
}

var sandboxTransitions = map[SandboxState][]SandboxState{
	SandboxPending:  {SandboxStarting, SandboxFailed},
	SandboxStarting: {SandboxRunning, SandboxFailed},
	SandboxRunning:  {SandboxStopping, SandboxFailed},
	SandboxStopping: {SandboxStopped, SandboxFailed},
	SandboxStopped:  {SandboxStarting, SandboxDestroyed},
	SandboxFailed:   {SandboxDestroyed},
}

func (s SandboxState) CanTransitionTo(target SandboxState) bool {
	for _, allowed := range sandboxTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

type NetworkMode string

const (
	NetworkIsolated  NetworkMode = "isolated"
	NetworkPublished NetworkMode = "published"
)

type Sandbox struct {
	SandboxID          string
	ProjectID          string
	Name               string
	State              SandboxState
	Backend            string
	BackendRef         string
	HostID             string
	SourceImageID      string
	ParentSnapshotID   string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          *time.Time
	CPULimit           *int
	MemoryLimitMB      *int
	NetworkMode        NetworkMode
	EnableBoostChannel bool
	Metadata           map[string]any
}

type SandboxFilter struct {
	ProjectID *string
	State     *SandboxState
	Backend   *string
}
