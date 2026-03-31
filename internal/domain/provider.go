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
	Name          string
	ImageRef      string
	Backend       string
	CPULimit      *int
	MemoryLimitMB *int
	NetworkMode   NetworkMode
	Metadata      map[string]any
}

type ExecRequest struct {
	Command []string
	Env     map[string]string
	WorkDir string
}

type SessionRequest struct {
	Shell string
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

	PublishPort(ctx context.Context, ref BackendRef, targetPort int, opts PublishPortOptions) (PublishedEndpoint, error)
	UnpublishPort(ctx context.Context, ref BackendRef, publishedPort int) error

	Health(ctx context.Context) ProviderHealth
}
