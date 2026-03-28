package domain

import "time"

type OperationState string

const (
	OpPending   OperationState = "pending"
	OpRunning   OperationState = "running"
	OpSucceeded OperationState = "succeeded"
	OpFailed    OperationState = "failed"
	OpCancelled OperationState = "cancelled"
)

func (s OperationState) Valid() bool {
	switch s {
	case OpPending, OpRunning, OpSucceeded, OpFailed, OpCancelled:
		return true
	}
	return false
}

func (s OperationState) Terminal() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled
}

type Operation struct {
	OperationID  string
	ResourceType string
	ResourceID   string
	SandboxID    string
	SnapshotID   string
	Type         string
	State        OperationState
	StartedAt    time.Time
	FinishedAt   *time.Time
	ErrorText    string
	Metadata     map[string]any
}

type OperationFilter struct {
	ResourceType *string
	ResourceID   *string
	SandboxID    *string
	State        *OperationState
	Limit        int
}
