package domain

import (
	"context"
	"time"
)

type ProjectStore interface {
	Create(ctx context.Context, p *Project) error
	Get(ctx context.Context, id string) (*Project, error)
	GetByName(ctx context.Context, name string) (*Project, error)
	List(ctx context.Context) ([]*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id string) error
}

type SandboxStore interface {
	Create(ctx context.Context, s *Sandbox) error
	Get(ctx context.Context, id string) (*Sandbox, error)
	List(ctx context.Context, f SandboxFilter) ([]*Sandbox, error)
	Update(ctx context.Context, s *Sandbox) error
	Delete(ctx context.Context, id string) error
	ListExpired(ctx context.Context, now time.Time) ([]*Sandbox, error)
}

type SnapshotStore interface {
	Create(ctx context.Context, s *Snapshot) error
	Get(ctx context.Context, id string) (*Snapshot, error)
	ListBySandbox(ctx context.Context, sandboxID string) ([]*Snapshot, error)
	Update(ctx context.Context, s *Snapshot) error
	Delete(ctx context.Context, id string) error
	ListOrphaned(ctx context.Context) ([]*Snapshot, error)
}

type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	ListBySandbox(ctx context.Context, sandboxID string) ([]*Session, error)
	Update(ctx context.Context, s *Session) error
	Delete(ctx context.Context, id string) error
}

type ImageStore interface {
	Create(ctx context.Context, i *BaseImage) error
	Get(ctx context.Context, id string) (*BaseImage, error)
	List(ctx context.Context, f ImageFilter) ([]*BaseImage, error)
	Update(ctx context.Context, i *BaseImage) error
	Delete(ctx context.Context, id string) error
}

type OperationStore interface {
	Create(ctx context.Context, o *Operation) error
	Get(ctx context.Context, id string) (*Operation, error)
	List(ctx context.Context, f OperationFilter) ([]*Operation, error)
	Update(ctx context.Context, o *Operation) error
	ListStale(ctx context.Context, olderThan time.Time) ([]*Operation, error)
	ListByState(ctx context.Context, state OperationState) ([]*Operation, error)
}

type PortBindingStore interface {
	Create(ctx context.Context, pb *PortBinding) error
	ListBySandbox(ctx context.Context, sandboxID string) ([]*PortBinding, error)
	Delete(ctx context.Context, sandboxID string, targetPort int) error
	GetByPublishedPort(ctx context.Context, publishedPort int) (*PortBinding, error)
	NextAvailablePort(ctx context.Context, rangeStart, rangeEnd int) (int, error)
}
