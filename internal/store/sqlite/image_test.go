package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

func TestImageCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	img := &domain.BaseImage{
		ImageID:      uuid.NewString(),
		Name:         "ubuntu",
		Version:      "24.04",
		SourceType:   domain.SourceImported,
		Backend:      "incus",
		Architecture: "amd64",
		State:        domain.ImageReady,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.ImageStore().Create(t.Context(), img); err != nil {
		t.Fatal(err)
	}
	got, err := s.ImageStore().Get(t.Context(), img.ImageID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ubuntu" || got.Version != "24.04" {
		t.Error("wrong image data")
	}
}

func TestImageListWithFilters(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"ubuntu", "debian", "ubuntu"} {
		s.ImageStore().Create(t.Context(), &domain.BaseImage{
			ImageID: uuid.NewString(), Name: name, Version: uuid.NewString()[:8],
			SourceType: domain.SourceImported, Backend: "incus",
			Architecture: "amd64", State: domain.ImageReady,
			CreatedAt: time.Now().UTC(),
		})
	}
	name := "ubuntu"
	list, _ := s.ImageStore().List(t.Context(), domain.ImageFilter{Name: &name})
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestImageDuplicateNameVersion(t *testing.T) {
	s := newTestStore(t)
	img1 := &domain.BaseImage{
		ImageID: uuid.NewString(), Name: "dup", Version: "1.0",
		SourceType: domain.SourceImported, Backend: "incus",
		Architecture: "amd64", State: domain.ImageReady,
		CreatedAt: time.Now().UTC(),
	}
	s.ImageStore().Create(t.Context(), img1)
	img2 := &domain.BaseImage{
		ImageID: uuid.NewString(), Name: "dup", Version: "1.0",
		SourceType: domain.SourceImported, Backend: "incus",
		Architecture: "amd64", State: domain.ImageReady,
		CreatedAt: time.Now().UTC(),
	}
	err := s.ImageStore().Create(t.Context(), img2)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestImageNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ImageStore().Get(t.Context(), "nope")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
