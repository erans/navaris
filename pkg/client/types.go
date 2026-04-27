// Package client provides a Go SDK for the Navaris sandbox control plane API.
package client

import "time"

// Project represents a project in the Navaris control plane.
type Project struct {
	ProjectID string         `json:"ProjectID"`
	Name      string         `json:"Name"`
	CreatedAt time.Time      `json:"CreatedAt"`
	UpdatedAt time.Time      `json:"UpdatedAt"`
	Metadata  map[string]any `json:"Metadata"`
}

// Sandbox represents a sandbox environment.
type Sandbox struct {
	SandboxID        string         `json:"SandboxID"`
	ProjectID        string         `json:"ProjectID"`
	Name             string         `json:"Name"`
	State            string         `json:"State"`
	Backend          string         `json:"Backend"`
	BackendRef       string         `json:"BackendRef"`
	HostID           string         `json:"HostID"`
	SourceImageID    string         `json:"SourceImageID"`
	ParentSnapshotID string         `json:"ParentSnapshotID"`
	CreatedAt        time.Time      `json:"CreatedAt"`
	UpdatedAt        time.Time      `json:"UpdatedAt"`
	ExpiresAt        *time.Time     `json:"ExpiresAt"`
	CPULimit         *int           `json:"CPULimit"`
	MemoryLimitMB    *int           `json:"MemoryLimitMB"`
	NetworkMode      string         `json:"NetworkMode"`
	Metadata         map[string]any `json:"Metadata"`
}

// Snapshot represents a point-in-time snapshot of a sandbox.
type Snapshot struct {
	SnapshotID      string         `json:"SnapshotID"`
	SandboxID       string         `json:"SandboxID"`
	Backend         string         `json:"Backend"`
	BackendRef      string         `json:"BackendRef"`
	Label           string         `json:"Label"`
	State           string         `json:"State"`
	CreatedAt       time.Time      `json:"CreatedAt"`
	UpdatedAt       time.Time      `json:"UpdatedAt"`
	ParentImageID   string         `json:"ParentImageID"`
	Publishable     bool           `json:"Publishable"`
	ConsistencyMode string         `json:"ConsistencyMode"`
	Metadata        map[string]any `json:"Metadata"`
}

// BaseImage represents a base image for creating sandboxes.
type BaseImage struct {
	ImageID          string         `json:"ImageID"`
	ProjectScope     string         `json:"ProjectScope"`
	Name             string         `json:"Name"`
	Version          string         `json:"Version"`
	SourceType       string         `json:"SourceType"`
	SourceSnapshotID string         `json:"SourceSnapshotID"`
	Backend          string         `json:"Backend"`
	BackendRef       string         `json:"BackendRef"`
	Architecture     string         `json:"Architecture"`
	State            string         `json:"State"`
	CreatedAt        time.Time      `json:"CreatedAt"`
	Metadata         map[string]any `json:"Metadata"`
}

// Session represents an interactive session attached to a sandbox.
type Session struct {
	SessionID      string         `json:"SessionID"`
	SandboxID      string         `json:"SandboxID"`
	Backing        string         `json:"Backing"`
	Shell          string         `json:"Shell"`
	State          string         `json:"State"`
	CreatedAt      time.Time      `json:"CreatedAt"`
	UpdatedAt      time.Time      `json:"UpdatedAt"`
	LastAttachedAt *time.Time     `json:"LastAttachedAt"`
	IdleTimeout    *int64         `json:"IdleTimeout"`
	Metadata       map[string]any `json:"Metadata"`
}

// Operation represents an asynchronous operation.
type Operation struct {
	OperationID  string         `json:"OperationID"`
	ResourceType string         `json:"ResourceType"`
	ResourceID   string         `json:"ResourceID"`
	SandboxID    string         `json:"SandboxID"`
	SnapshotID   string         `json:"SnapshotID"`
	Type         string         `json:"Type"`
	State        OperationState `json:"State"`
	StartedAt    time.Time      `json:"StartedAt"`
	FinishedAt   *time.Time     `json:"FinishedAt"`
	ErrorText    string         `json:"ErrorText"`
	Metadata     map[string]any `json:"Metadata"`
}

// OperationState represents the state of an asynchronous operation.
type OperationState string

const (
	OpPending   OperationState = "pending"
	OpRunning   OperationState = "running"
	OpSucceeded OperationState = "succeeded"
	OpFailed    OperationState = "failed"
	OpCancelled OperationState = "cancelled"
)

// Terminal reports whether the operation has reached a final state.
func (s OperationState) Terminal() bool {
	return s == OpSucceeded || s == OpFailed || s == OpCancelled
}

// PortBinding represents a published port mapping for a sandbox.
type PortBinding struct {
	SandboxID     string    `json:"SandboxID"`
	TargetPort    int       `json:"TargetPort"`
	PublishedPort int       `json:"PublishedPort"`
	HostAddress   string    `json:"HostAddress"`
	CreatedAt     time.Time `json:"CreatedAt"`
}

// ProviderHealth represents the health status of a backend provider.
type ProviderHealth struct {
	Backend   string `json:"Backend"`
	Healthy   bool   `json:"Healthy"`
	LatencyMS int64  `json:"LatencyMS"`
	Error     string `json:"Error"`
}

// --- Request types ---

// CreateProjectRequest is the request body for creating a project.
type CreateProjectRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// UpdateProjectRequest is the request body for updating a project.
type UpdateProjectRequest struct {
	Name     string         `json:"name"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// CreateSandboxRequest is the request body for creating a sandbox from an image.
type CreateSandboxRequest struct {
	ProjectID          string         `json:"project_id"`
	Name               string         `json:"name"`
	ImageID            string         `json:"image_id,omitempty"`
	Backend            string         `json:"backend,omitempty"`
	CPULimit           *int           `json:"cpu_limit,omitempty"`
	MemoryLimitMB      *int           `json:"memory_limit_mb,omitempty"`
	NetworkMode        string         `json:"network_mode,omitempty"`
	EnableBoostChannel *bool          `json:"enable_boost_channel,omitempty"`
	ExpiresAt          *time.Time     `json:"expires_at,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

// CreateSandboxFromSnapshotRequest is the request body for creating a sandbox from a snapshot.
type CreateSandboxFromSnapshotRequest struct {
	ProjectID     string         `json:"project_id"`
	Name          string         `json:"name"`
	SnapshotID    string         `json:"snapshot_id"`
	CPULimit      *int           `json:"cpu_limit,omitempty"`
	MemoryLimitMB *int           `json:"memory_limit_mb,omitempty"`
	NetworkMode   string         `json:"network_mode,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// CreateSnapshotRequest is the request body for creating a snapshot.
type CreateSnapshotRequest struct {
	Label           string `json:"label"`
	ConsistencyMode string `json:"consistency_mode,omitempty"`
}

// CreateImageRequest is the request body for promoting a snapshot to an image.
type CreateImageRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

// RegisterImageRequest is the request body for registering an external image.
type RegisterImageRequest struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Backend      string `json:"backend"`
	BackendRef   string `json:"backend_ref"`
	Architecture string `json:"architecture,omitempty"`
}

// CreateSessionRequest is the request body for creating a session.
type CreateSessionRequest struct {
	Backing string `json:"backing,omitempty"`
	Shell   string `json:"shell,omitempty"`
}

// CreatePortRequest is the request body for publishing a port.
type CreatePortRequest struct {
	TargetPort int `json:"target_port"`
}

// ExecRequest is the request body for executing a command in a sandbox.
type ExecRequest struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
}

// ExecResponse is the response from executing a command in a sandbox.
type ExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// StopSandboxRequest is the request body for stopping a sandbox.
type StopSandboxRequest struct {
	Force bool `json:"force"`
}

// WaitOptions configures how WaitForOperation behaves.
type WaitOptions struct {
	Timeout time.Duration
}

// DefaultWaitTimeout is the default timeout for WaitForOperation.
const DefaultWaitTimeout = 5 * time.Minute

// listResponse is the envelope used by list endpoints.
type listResponse[T any] struct {
	Data       []T `json:"data"`
	Pagination any `json:"pagination"`
}
