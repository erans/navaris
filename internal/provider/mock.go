package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

var _ domain.Provider = (*MockProvider)(nil)

type MockProvider struct {
	CreateSandboxFn             func(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error)
	StartSandboxFn              func(ctx context.Context, ref domain.BackendRef) error
	StopSandboxFn               func(ctx context.Context, ref domain.BackendRef, force bool) error
	DestroySandboxFn            func(ctx context.Context, ref domain.BackendRef) error
	GetSandboxStateFn           func(ctx context.Context, ref domain.BackendRef) (domain.SandboxState, error)
	ExecFn                      func(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error)
	ExecDetachedFn              func(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error)
	AttachSessionFn             func(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error)
	CreateSnapshotFn            func(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error)
	RestoreSnapshotFn           func(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error
	DeleteSnapshotFn            func(ctx context.Context, snapshotRef domain.BackendRef) error
	CreateSandboxFromSnapshotFn func(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error)
	PublishSnapshotAsImageFn    func(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error)
	DeleteImageFn               func(ctx context.Context, imageRef domain.BackendRef) error
	GetImageInfoFn              func(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error)
	PublishPortFn               func(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error)
	UnpublishPortFn             func(ctx context.Context, ref domain.BackendRef, publishedPort int) error
	HealthFn                    func(ctx context.Context) domain.ProviderHealth
	ForkSandboxFn               func(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error)
}

func NewMock() *MockProvider {
	return &MockProvider{
		CreateSandboxFn: func(_ context.Context, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
			return domain.BackendRef{Backend: "mock", Ref: "mock-" + uuid.NewString()[:8]}, nil
		},
		StartSandboxFn:    func(_ context.Context, _ domain.BackendRef) error { return nil },
		StopSandboxFn:     func(_ context.Context, _ domain.BackendRef, _ bool) error { return nil },
		DestroySandboxFn:  func(_ context.Context, _ domain.BackendRef) error { return nil },
		GetSandboxStateFn: func(_ context.Context, _ domain.BackendRef) (domain.SandboxState, error) { return domain.SandboxRunning, nil },
		ExecFn: func(_ context.Context, _ domain.BackendRef, _ domain.ExecRequest) (domain.ExecHandle, error) {
			return domain.ExecHandle{
				Stdout: io.NopCloser(io.LimitReader(nil, 0)),
				Stderr: io.NopCloser(io.LimitReader(nil, 0)),
				Wait:   func() (int, error) { return 0, nil },
				Cancel: func() error { return nil },
			}, nil
		},
		ExecDetachedFn: func(_ context.Context, _ domain.BackendRef, _ domain.ExecRequest) (domain.DetachedExecHandle, error) {
			return domain.DetachedExecHandle{}, nil
		},
		AttachSessionFn: func(_ context.Context, _ domain.BackendRef, _ domain.SessionRequest) (domain.SessionHandle, error) {
			return domain.SessionHandle{}, fmt.Errorf("mock provider does not support attach")
		},
		CreateSnapshotFn: func(_ context.Context, _ domain.BackendRef, _ string, _ domain.ConsistencyMode) (domain.BackendRef, error) {
			return domain.BackendRef{Backend: "mock", Ref: "snap-" + uuid.NewString()[:8]}, nil
		},
		RestoreSnapshotFn: func(_ context.Context, _ domain.BackendRef, _ domain.BackendRef) error { return nil },
		DeleteSnapshotFn:  func(_ context.Context, _ domain.BackendRef) error { return nil },
		CreateSandboxFromSnapshotFn: func(_ context.Context, _ domain.BackendRef, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
			return domain.BackendRef{Backend: "mock", Ref: "mock-" + uuid.NewString()[:8]}, nil
		},
		PublishSnapshotAsImageFn: func(_ context.Context, _ domain.BackendRef, _ domain.PublishImageRequest) (domain.BackendRef, error) {
			return domain.BackendRef{Backend: "mock", Ref: "img-" + uuid.NewString()[:8]}, nil
		},
		DeleteImageFn:  func(_ context.Context, _ domain.BackendRef) error { return nil },
		GetImageInfoFn: func(_ context.Context, _ domain.BackendRef) (domain.ImageInfo, error) { return domain.ImageInfo{Architecture: "amd64"}, nil },
		PublishPortFn: func(_ context.Context, _ domain.BackendRef, targetPort int, _ domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
			return domain.PublishedEndpoint{HostAddress: "0.0.0.0", PublishedPort: 40000 + targetPort}, nil
		},
		UnpublishPortFn: func(_ context.Context, _ domain.BackendRef, _ int) error { return nil },
		HealthFn:        func(_ context.Context) domain.ProviderHealth { return domain.ProviderHealth{Backend: "mock", Healthy: true} },
		ForkSandboxFn: func(_ context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
			if count < 1 {
				return nil, fmt.Errorf("mock fork: count must be >= 1")
			}
			out := make([]domain.BackendRef, 0, count)
			for i := 0; i < count; i++ {
				out = append(out, domain.BackendRef{
					Backend: parent.Backend,
					Ref:     fmt.Sprintf("mock-fork-%s-%d", uuid.NewString()[:8], i),
				})
			}
			return out, nil
		},
	}
}

func (m *MockProvider) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	return m.CreateSandboxFn(ctx, req)
}
func (m *MockProvider) StartSandbox(ctx context.Context, ref domain.BackendRef) error {
	return m.StartSandboxFn(ctx, ref)
}
func (m *MockProvider) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) error {
	return m.StopSandboxFn(ctx, ref, force)
}
func (m *MockProvider) DestroySandbox(ctx context.Context, ref domain.BackendRef) error {
	return m.DestroySandboxFn(ctx, ref)
}
func (m *MockProvider) GetSandboxState(ctx context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
	return m.GetSandboxStateFn(ctx, ref)
}
func (m *MockProvider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	return m.ExecFn(ctx, ref, req)
}
func (m *MockProvider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	return m.ExecDetachedFn(ctx, ref, req)
}
func (m *MockProvider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	return m.AttachSessionFn(ctx, ref, req)
}
func (m *MockProvider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	return m.CreateSnapshotFn(ctx, ref, label, mode)
}
func (m *MockProvider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	return m.RestoreSnapshotFn(ctx, sandboxRef, snapshotRef)
}
func (m *MockProvider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	return m.DeleteSnapshotFn(ctx, snapshotRef)
}
func (m *MockProvider) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	return m.CreateSandboxFromSnapshotFn(ctx, snapshotRef, req)
}
func (m *MockProvider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	return m.PublishSnapshotAsImageFn(ctx, snapshotRef, req)
}
func (m *MockProvider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	return m.DeleteImageFn(ctx, imageRef)
}
func (m *MockProvider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	return m.GetImageInfoFn(ctx, imageRef)
}
func (m *MockProvider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	return m.PublishPortFn(ctx, ref, targetPort, opts)
}
func (m *MockProvider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	return m.UnpublishPortFn(ctx, ref, publishedPort)
}
func (m *MockProvider) Health(ctx context.Context) domain.ProviderHealth {
	return m.HealthFn(ctx)
}
func (m *MockProvider) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	return m.ForkSandboxFn(ctx, parent, count)
}
