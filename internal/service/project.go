package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
)

type ProjectService struct {
	projects domain.ProjectStore
}

func NewProjectService(projects domain.ProjectStore) *ProjectService {
	return &ProjectService{projects: projects}
}

func (s *ProjectService) Create(ctx context.Context, name string, metadata map[string]any) (*domain.Project, error) {
	now := time.Now().UTC()
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  metadata,
	}
	if err := s.projects.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) Get(ctx context.Context, id string) (*domain.Project, error) {
	return s.projects.Get(ctx, id)
}

func (s *ProjectService) GetByName(ctx context.Context, name string) (*domain.Project, error) {
	return s.projects.GetByName(ctx, name)
}

func (s *ProjectService) List(ctx context.Context) ([]*domain.Project, error) {
	return s.projects.List(ctx)
}

func (s *ProjectService) Update(ctx context.Context, id string, name string, metadata map[string]any) (*domain.Project, error) {
	p, err := s.projects.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Name = name
	p.UpdatedAt = time.Now().UTC()
	p.Metadata = metadata
	if err := s.projects.Update(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) Delete(ctx context.Context, id string) error {
	return s.projects.Delete(ctx, id)
}
