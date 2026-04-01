package provider_test

import (
	"context"
	"strings"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider"
)

func TestRegistryDispatchByBackend(t *testing.T) {
	reg := provider.NewRegistry()

	incusCalled := false
	firecrackerCalled := false

	incus := provider.NewMock()
	incus.StartSandboxFn = func(_ context.Context, ref domain.BackendRef) error {
		incusCalled = true
		return nil
	}

	firecracker := provider.NewMock()
	firecracker.StartSandboxFn = func(_ context.Context, ref domain.BackendRef) error {
		firecrackerCalled = true
		return nil
	}

	reg.Register("incus", incus)
	reg.Register("firecracker", firecracker)

	ctx := context.Background()

	if err := reg.StartSandbox(ctx, domain.BackendRef{Backend: "incus", Ref: "sb-1"}); err != nil {
		t.Fatalf("StartSandbox incus: %v", err)
	}
	if !incusCalled {
		t.Error("expected incus provider to be called")
	}
	if firecrackerCalled {
		t.Error("firecracker should not have been called")
	}

	if err := reg.StartSandbox(ctx, domain.BackendRef{Backend: "firecracker", Ref: "sb-2"}); err != nil {
		t.Fatalf("StartSandbox firecracker: %v", err)
	}
	if !firecrackerCalled {
		t.Error("expected firecracker provider to be called")
	}
}

func TestRegistryUnknownBackend(t *testing.T) {
	reg := provider.NewRegistry()
	reg.Register("incus", provider.NewMock())

	ctx := context.Background()
	err := reg.StartSandbox(ctx, domain.BackendRef{Backend: "unknown", Ref: "sb-1"})
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention the backend name, got: %v", err)
	}
}

func TestRegistryCreateSandboxUsesFallback(t *testing.T) {
	reg := provider.NewRegistry()

	called := ""
	mk := provider.NewMock()
	mk.CreateSandboxFn = func(_ context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
		called = "incus"
		return domain.BackendRef{Backend: "incus", Ref: "sb-fallback"}, nil
	}
	reg.Register("incus", mk)
	reg.SetFallback("incus")

	ctx := context.Background()
	ref, err := reg.CreateSandbox(ctx, domain.CreateSandboxRequest{
		Name:     "test",
		ImageRef: "debian:12",
		Backend:  "", // empty — should use fallback
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if called != "incus" {
		t.Errorf("expected incus to be called via fallback, called=%q", called)
	}
	if ref.Backend != "incus" {
		t.Errorf("expected ref.Backend=incus, got %q", ref.Backend)
	}
}

func TestRegistryCreateSandboxExplicitBackend(t *testing.T) {
	reg := provider.NewRegistry()

	incusCalled := false
	firecrackerCalled := false

	incus := provider.NewMock()
	incus.CreateSandboxFn = func(_ context.Context, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
		incusCalled = true
		return domain.BackendRef{Backend: "incus", Ref: "sb-incus"}, nil
	}
	firecracker := provider.NewMock()
	firecracker.CreateSandboxFn = func(_ context.Context, _ domain.CreateSandboxRequest) (domain.BackendRef, error) {
		firecrackerCalled = true
		return domain.BackendRef{Backend: "firecracker", Ref: "sb-fc"}, nil
	}

	reg.Register("incus", incus)
	reg.Register("firecracker", firecracker)
	reg.SetFallback("incus")

	ctx := context.Background()
	ref, err := reg.CreateSandbox(ctx, domain.CreateSandboxRequest{
		Name:     "test",
		ImageRef: "debian:12",
		Backend:  "firecracker", // explicit override
	})
	if err != nil {
		t.Fatalf("CreateSandbox: %v", err)
	}
	if incusCalled {
		t.Error("incus (fallback) should not have been called when Backend is explicit")
	}
	if !firecrackerCalled {
		t.Error("expected firecracker to be called")
	}
	if ref.Backend != "firecracker" {
		t.Errorf("expected ref.Backend=firecracker, got %q", ref.Backend)
	}
}

func TestRegistryHealthAggregates(t *testing.T) {
	reg := provider.NewRegistry()

	incus := provider.NewMock()
	incus.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "incus", Healthy: true}
	}
	firecracker := provider.NewMock()
	firecracker.HealthFn = func(_ context.Context) domain.ProviderHealth {
		return domain.ProviderHealth{Backend: "firecracker", Healthy: false, Error: "firecracker down"}
	}

	reg.Register("incus", incus)
	reg.Register("firecracker", firecracker)

	ctx := context.Background()
	h := reg.Health(ctx)

	if !h.Healthy {
		t.Error("expected Healthy=true when at least one provider is healthy")
	}

	// Backend should contain both names (sorted)
	if !strings.Contains(h.Backend, "firecracker") {
		t.Errorf("expected Backend to contain 'firecracker', got %q", h.Backend)
	}
	if !strings.Contains(h.Backend, "incus") {
		t.Errorf("expected Backend to contain 'incus', got %q", h.Backend)
	}

	// Names should be sorted: firecracker before incus
	fcIdx := strings.Index(h.Backend, "firecracker")
	incusIdx := strings.Index(h.Backend, "incus")
	if fcIdx > incusIdx {
		t.Errorf("expected 'firecracker' before 'incus' in Backend=%q (sorted)", h.Backend)
	}

	// Error from unhealthy provider should be included
	if !strings.Contains(h.Error, "firecracker down") {
		t.Errorf("expected error to include 'firecracker down', got %q", h.Error)
	}
}

func TestRegistryLen(t *testing.T) {
	reg := provider.NewRegistry()

	if reg.Len() != 0 {
		t.Errorf("expected Len=0, got %d", reg.Len())
	}

	reg.Register("incus", provider.NewMock())
	if reg.Len() != 1 {
		t.Errorf("expected Len=1, got %d", reg.Len())
	}

	reg.Register("firecracker", provider.NewMock())
	if reg.Len() != 2 {
		t.Errorf("expected Len=2, got %d", reg.Len())
	}

	if !reg.Has("incus") {
		t.Error("expected Has('incus')=true")
	}
	if reg.Has("unknown") {
		t.Error("expected Has('unknown')=false")
	}
}
