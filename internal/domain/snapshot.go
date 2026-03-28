package domain

import "time"

type SnapshotState string

const (
	SnapshotPending  SnapshotState = "pending"
	SnapshotCreating SnapshotState = "creating"
	SnapshotReady    SnapshotState = "ready"
	SnapshotFailed   SnapshotState = "failed"
	SnapshotDeleted  SnapshotState = "deleted"
)

func (s SnapshotState) Valid() bool {
	switch s {
	case SnapshotPending, SnapshotCreating, SnapshotReady, SnapshotFailed, SnapshotDeleted:
		return true
	}
	return false
}

var snapshotTransitions = map[SnapshotState][]SnapshotState{
	SnapshotPending:  {SnapshotCreating, SnapshotFailed},
	SnapshotCreating: {SnapshotReady, SnapshotFailed},
	SnapshotReady:    {SnapshotDeleted},
	SnapshotFailed:   {SnapshotDeleted},
}

func (s SnapshotState) CanTransitionTo(target SnapshotState) bool {
	for _, allowed := range snapshotTransitions[s] {
		if allowed == target {
			return true
		}
	}
	return false
}

type ConsistencyMode string

const (
	ConsistencyStopped ConsistencyMode = "stopped"
	ConsistencyLive    ConsistencyMode = "live"
)

type Snapshot struct {
	SnapshotID      string
	SandboxID       string
	Backend         string
	BackendRef      string
	Label           string
	State           SnapshotState
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ParentImageID   string
	Publishable     bool
	ConsistencyMode ConsistencyMode
	Metadata        map[string]any
}
