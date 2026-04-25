package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/navaris/navaris/internal/domain"
)

// fakeProvider is a minimal in-test stub used solely to verify dispatch.
// It only implements ForkSandbox; other methods panic if called.
type fakeProvider struct {
	domain.Provider // embed for the methods we don't care about (will be nil)
	forkResp        []domain.BackendRef
	forkErr         error
	gotCount        int
	gotParent       domain.BackendRef
}

func (f *fakeProvider) ForkSandbox(ctx context.Context, parent domain.BackendRef, count int) ([]domain.BackendRef, error) {
	f.gotCount = count
	f.gotParent = parent
	return f.forkResp, f.forkErr
}

func TestRegistry_ForkSandbox_DispatchesByBackend(t *testing.T) {
	r := NewRegistry()
	want := []domain.BackendRef{{Backend: "fakebackend", Ref: "child-1"}}
	f := &fakeProvider{forkResp: want}
	r.Register("fakebackend", f)

	got, err := r.ForkSandbox(context.Background(), domain.BackendRef{Backend: "fakebackend", Ref: "p"}, 1)
	if err != nil {
		t.Fatalf("ForkSandbox: %v", err)
	}
	if len(got) != 1 || got[0].Ref != "child-1" {
		t.Errorf("unexpected children: %+v", got)
	}
	if f.gotCount != 1 {
		t.Errorf("expected count=1 forwarded, got %d", f.gotCount)
	}
	if f.gotParent.Ref != "p" {
		t.Errorf("expected parent.Ref=p forwarded, got %q", f.gotParent.Ref)
	}
}

func TestRegistry_ForkSandbox_UnknownBackendErrors(t *testing.T) {
	r := NewRegistry()
	_, err := r.ForkSandbox(context.Background(), domain.BackendRef{Backend: "absent", Ref: "p"}, 1)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestErrNotSupported_IsSentinel(t *testing.T) {
	wrapped := errors.Join(domain.ErrNotSupported, errors.New("incus: containers do not have VM memory"))
	if !errors.Is(wrapped, domain.ErrNotSupported) {
		t.Errorf("errors.Join must preserve ErrNotSupported")
	}
}
