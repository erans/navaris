package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/navaris/navaris/internal/domain"
)

var _ domain.Provider = (*Registry)(nil)

// Registry implements domain.Provider by dispatching to named backend providers.
type Registry struct {
	providers map[string]domain.Provider
	fallback  string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]domain.Provider),
	}
}

// Register adds a provider under the given name.
func (r *Registry) Register(name string, p domain.Provider) {
	r.providers[name] = p
}

// Len returns the number of registered providers.
func (r *Registry) Len() int {
	return len(r.providers)
}

// Has reports whether a provider with the given name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.providers[name]
	return ok
}

// SetFallback sets the default backend used when a request does not specify one.
func (r *Registry) SetFallback(name string) {
	r.fallback = name
}

// Fallback returns the current default backend name.
func (r *Registry) Fallback() string {
	return r.fallback
}

// resolve returns the provider for the given backend name.
// If backend is empty, the configured fallback is used.
func (r *Registry) resolve(backend string) (domain.Provider, error) {
	if backend == "" {
		backend = r.fallback
	}
	p, ok := r.providers[backend]
	if !ok {
		return nil, fmt.Errorf("provider %q not available", backend)
	}
	return p, nil
}

// CreateSandbox dispatches by req.Backend (falls back to r.fallback if empty).
func (r *Registry) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	p, err := r.resolve(req.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSandbox(ctx, req)
}

// StartSandbox dispatches by ref.Backend.
func (r *Registry) StartSandbox(ctx context.Context, ref domain.BackendRef) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.StartSandbox(ctx, ref)
}

// StopSandbox dispatches by ref.Backend.
func (r *Registry) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.StopSandbox(ctx, ref, force)
}

// DestroySandbox dispatches by ref.Backend.
func (r *Registry) DestroySandbox(ctx context.Context, ref domain.BackendRef) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.DestroySandbox(ctx, ref)
}

// GetSandboxState dispatches by ref.Backend.
func (r *Registry) GetSandboxState(ctx context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return "", err
	}
	return p.GetSandboxState(ctx, ref)
}

// Exec dispatches by ref.Backend.
func (r *Registry) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.ExecHandle{}, err
	}
	return p.Exec(ctx, ref, req)
}

// ExecDetached dispatches by ref.Backend.
func (r *Registry) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.DetachedExecHandle{}, err
	}
	return p.ExecDetached(ctx, ref, req)
}

// AttachSession dispatches by ref.Backend.
func (r *Registry) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.SessionHandle{}, err
	}
	return p.AttachSession(ctx, ref, req)
}

// CreateSnapshot dispatches by ref.Backend.
func (r *Registry) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSnapshot(ctx, ref, label, mode)
}

// RestoreSnapshot dispatches by sandboxRef.Backend.
func (r *Registry) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	p, err := r.resolve(sandboxRef.Backend)
	if err != nil {
		return err
	}
	return p.RestoreSnapshot(ctx, sandboxRef, snapshotRef)
}

// DeleteSnapshot dispatches by snapshotRef.Backend.
func (r *Registry) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return err
	}
	return p.DeleteSnapshot(ctx, snapshotRef)
}

// CreateSandboxFromSnapshot dispatches by snapshotRef.Backend.
func (r *Registry) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.CreateSandboxFromSnapshot(ctx, snapshotRef, req)
}

// ForkSandbox dispatches by parent.Backend.
func (r *Registry) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	p, err := r.resolve(parent.Backend)
	if err != nil {
		return nil, err
	}
	return p.ForkSandbox(ctx, parent, count)
}

// PublishSnapshotAsImage dispatches by snapshotRef.Backend.
func (r *Registry) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	p, err := r.resolve(snapshotRef.Backend)
	if err != nil {
		return domain.BackendRef{}, err
	}
	return p.PublishSnapshotAsImage(ctx, snapshotRef, req)
}

// DeleteImage dispatches by imageRef.Backend.
func (r *Registry) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	p, err := r.resolve(imageRef.Backend)
	if err != nil {
		return err
	}
	return p.DeleteImage(ctx, imageRef)
}

// GetImageInfo dispatches by imageRef.Backend.
func (r *Registry) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	p, err := r.resolve(imageRef.Backend)
	if err != nil {
		return domain.ImageInfo{}, err
	}
	return p.GetImageInfo(ctx, imageRef)
}

// PublishPort dispatches by ref.Backend.
func (r *Registry) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return domain.PublishedEndpoint{}, err
	}
	return p.PublishPort(ctx, ref, targetPort, opts)
}

// UnpublishPort dispatches by ref.Backend.
func (r *Registry) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	p, err := r.resolve(ref.Backend)
	if err != nil {
		return err
	}
	return p.UnpublishPort(ctx, ref, publishedPort)
}

// Health aggregates health from all registered providers, sorted by name.
// Healthy is true if at least one provider is healthy.
// Backend is a comma-joined list of all provider names.
// Error joins all non-empty error strings.
func (r *Registry) Health(ctx context.Context) domain.ProviderHealth {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	anyHealthy := false
	var errs []string

	for _, name := range names {
		h := r.providers[name].Health(ctx)
		if h.Healthy {
			anyHealthy = true
		}
		if h.Error != "" {
			errs = append(errs, h.Error)
		}
	}

	return domain.ProviderHealth{
		Backend: strings.Join(names, ","),
		Healthy: anyHealthy,
		Error:   strings.Join(errs, "; "),
	}
}
