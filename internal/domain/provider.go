package domain

import (
	"context"
	"io"
)

type BackendRef struct {
	Backend string
	Ref     string
}

type CreateSandboxRequest struct {
	Name               string
	ImageRef           string
	Backend            string
	CPULimit           *int
	MemoryLimitMB      *int
	NetworkMode        NetworkMode
	EnableBoostChannel *bool  // nil = caller did not specify; service materializes to daemon flag value
	SandboxID          string // navaris-side sandbox UUID; service layer fills this in. Provider uses it as the boost-channel identity (vs. BackendRef which is the FC vmID / Incus container name).
	Metadata           map[string]any
}

type ExecRequest struct {
	Command []string
	Env     map[string]string
	WorkDir string
}

type SessionRequest struct {
	Shell   string
	Command []string // Full command with args; takes precedence over Shell if non-empty.
}

type PublishPortOptions struct{}

type PublishImageRequest struct {
	Name    string
	Version string
}

type PublishedEndpoint struct {
	HostAddress   string
	PublishedPort int
}

// UpdateResourcesRequest carries the desired new CPU/memory limits for a
// running sandbox. A nil pointer means "leave unchanged". The service layer
// rejects requests where both fields are nil.
type UpdateResourcesRequest struct {
	CPULimit      *int
	MemoryLimitMB *int
}

// ProviderResizeError is returned from Provider.UpdateResources when the
// backend cannot apply the requested change live. The Reason is a stable,
// machine-readable code; Detail is a human-readable supplement.
type ProviderResizeError struct {
	Reason string
	Detail string
}

func (e *ProviderResizeError) Error() string {
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}

const (
	ResizeReasonExceedsCeiling          = "exceeds_ceiling"
	ResizeReasonCPUUnsupportedByBackend = "cpu_resize_unsupported_by_backend"
	ResizeReasonBackendRejected         = "backend_rejected"
)

type ExecHandle struct {
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	Wait   func() (exitCode int, err error)
	Cancel func() error
}

type DetachedExecHandle struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Resize func(w, h int) error
	Close  func() error
}

type SessionHandle struct {
	Conn   io.ReadWriteCloser
	Resize func(w, h int) error
	Close  func() error
}

type ImageInfo struct {
	Architecture string
	Size         int64
}

type ProviderHealth struct {
	Backend   string
	Healthy   bool
	LatencyMS int64
	Error     string
}

type Provider interface {
	CreateSandbox(ctx context.Context, req CreateSandboxRequest) (BackendRef, error)
	StartSandbox(ctx context.Context, ref BackendRef) error
	StopSandbox(ctx context.Context, ref BackendRef, force bool) error
	DestroySandbox(ctx context.Context, ref BackendRef) error
	GetSandboxState(ctx context.Context, ref BackendRef) (SandboxState, error)

	Exec(ctx context.Context, ref BackendRef, req ExecRequest) (ExecHandle, error)
	ExecDetached(ctx context.Context, ref BackendRef, req ExecRequest) (DetachedExecHandle, error)
	AttachSession(ctx context.Context, ref BackendRef, req SessionRequest) (SessionHandle, error)

	CreateSnapshot(ctx context.Context, ref BackendRef, label string, mode ConsistencyMode) (BackendRef, error)
	RestoreSnapshot(ctx context.Context, sandboxRef BackendRef, snapshotRef BackendRef) error
	DeleteSnapshot(ctx context.Context, snapshotRef BackendRef) error

	CreateSandboxFromSnapshot(ctx context.Context, snapshotRef BackendRef, req CreateSandboxRequest) (BackendRef, error)
	PublishSnapshotAsImage(ctx context.Context, snapshotRef BackendRef, req PublishImageRequest) (BackendRef, error)
	DeleteImage(ctx context.Context, imageRef BackendRef) error
	GetImageInfo(ctx context.Context, imageRef BackendRef) (ImageInfo, error)

	// ForkSandbox creates count children from a running parent. Implementations
	// that don't support this (e.g. container-only providers) MUST return an
	// error wrapping domain.ErrNotSupported and an empty slice.
	ForkSandbox(ctx context.Context, parent BackendRef, count int) ([]BackendRef, error)

	// UpdateResources applies new CPU/memory limits to a running sandbox.
	// Returns *ProviderResizeError when the backend cannot apply the change
	// live (the service layer maps that to HTTP 409). Other errors are
	// treated as backend failures.
	UpdateResources(ctx context.Context, ref BackendRef, req UpdateResourcesRequest) error

	PublishPort(ctx context.Context, ref BackendRef, targetPort int, opts PublishPortOptions) (PublishedEndpoint, error)
	UnpublishPort(ctx context.Context, ref BackendRef, publishedPort int) error

	Health(ctx context.Context) ProviderHealth
}
