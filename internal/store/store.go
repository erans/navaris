package store

import "github.com/navaris/navaris/internal/domain"

type Store interface {
	ProjectStore() domain.ProjectStore
	SandboxStore() domain.SandboxStore
	SnapshotStore() domain.SnapshotStore
	SessionStore() domain.SessionStore
	ImageStore() domain.ImageStore
	OperationStore() domain.OperationStore
	PortBindingStore() domain.PortBindingStore
	Close() error
}
