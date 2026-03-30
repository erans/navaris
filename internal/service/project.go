package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type ProjectService struct {
	projects domain.ProjectStore
}

func NewProjectService(projects domain.ProjectStore) *ProjectService {
	return &ProjectService{projects: projects}
}

func (s *ProjectService) Create(ctx context.Context, name string, metadata map[string]any) (*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CreateProject")
	defer span.End()

	now := time.Now().UTC()
	p := &domain.Project{
		ProjectID: uuid.NewString(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  metadata,
	}
	if err := s.projects.Create(ctx, p); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) Get(ctx context.Context, id string) (*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetProject")
	defer span.End()

	span.SetAttributes(attribute.String("project.id", id))
	p, err := s.projects.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) GetByName(ctx context.Context, name string) (*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetProjectByName")
	defer span.End()

	p, err := s.projects.GetByName(ctx, name)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) List(ctx context.Context) ([]*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ListProjects")
	defer span.End()

	list, err := s.projects.List(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return list, nil
}

func (s *ProjectService) Update(ctx context.Context, id string, name string, metadata map[string]any) (*domain.Project, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.UpdateProject")
	defer span.End()

	span.SetAttributes(attribute.String("project.id", id))
	p, err := s.projects.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	p.Name = name
	p.UpdatedAt = time.Now().UTC()
	p.Metadata = metadata
	if err := s.projects.Update(ctx, p); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return p, nil
}

func (s *ProjectService) Delete(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DeleteProject")
	defer span.End()

	span.SetAttributes(attribute.String("project.id", id))
	if err := s.projects.Delete(ctx, id); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}
