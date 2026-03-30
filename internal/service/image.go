package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type ImageService struct {
	images    domain.ImageStore
	snapshots domain.SnapshotStore
	ops       domain.OperationStore
	provider  domain.Provider
	events    domain.EventBus
	workers   *worker.Dispatcher
}

func NewImageService(
	images domain.ImageStore,
	snapshots domain.SnapshotStore,
	ops domain.OperationStore,
	provider domain.Provider,
	events domain.EventBus,
	workers *worker.Dispatcher,
) *ImageService {
	svc := &ImageService{
		images: images, snapshots: snapshots, ops: ops,
		provider: provider, events: events, workers: workers,
	}
	svc.workers.Register("promote_snapshot", svc.handlePromote)
	svc.workers.Register("delete_image", svc.handleDelete)
	return svc
}

func (s *ImageService) PromoteSnapshot(ctx context.Context, snapshotID, name, version string) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.PromoteSnapshotToImage")
	defer span.End()

	span.SetAttributes(attribute.String("snapshot.id", snapshotID))

	snap, err := s.snapshots.Get(ctx, snapshotID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if snap.State != domain.SnapshotReady {
		err := fmt.Errorf("cannot promote snapshot in state %s: %w", snap.State, domain.ErrInvalidState)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	now := time.Now().UTC()
	img := &domain.BaseImage{
		ImageID:          uuid.NewString(),
		Name:             name,
		Version:          version,
		SourceType:       domain.SourceSnapshotPromoted,
		SourceSnapshotID: snapshotID,
		Backend:          snap.Backend,
		Architecture:     "amd64", // will be updated by handler
		State:            domain.ImagePending,
		CreatedAt:        now,
	}
	if err := s.images.Create(ctx, img); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "image",
		ResourceID:   img.ImageID,
		SnapshotID:   snapshotID,
		Type:         "promote_snapshot",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *ImageService) Register(ctx context.Context, name, version, backend, backendRef, arch string) (*domain.BaseImage, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.RegisterImage")
	defer span.End()

	span.SetAttributes(attribute.String("image.ref", backendRef))

	now := time.Now().UTC()
	img := &domain.BaseImage{
		ImageID:      uuid.NewString(),
		Name:         name,
		Version:      version,
		SourceType:   domain.SourceImported,
		Backend:      backend,
		BackendRef:   backendRef,
		Architecture: arch,
		State:        domain.ImageReady,
		CreatedAt:    now,
	}
	if err := s.images.Create(ctx, img); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return img, nil
}

func (s *ImageService) Get(ctx context.Context, id string) (*domain.BaseImage, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetImage")
	defer span.End()

	img, err := s.images.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return img, nil
}

func (s *ImageService) List(ctx context.Context, filter domain.ImageFilter) ([]*domain.BaseImage, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ListImages")
	defer span.End()

	list, err := s.images.List(ctx, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return list, nil
}

func (s *ImageService) Delete(ctx context.Context, id string) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.DeleteImage")
	defer span.End()

	img, err := s.images.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	now := time.Now().UTC()
	op := &domain.Operation{
		OperationID:  uuid.NewString(),
		ResourceType: "image",
		ResourceID:   img.ImageID,
		Type:         "delete_image",
		State:        domain.OpPending,
		StartedAt:    now,
	}
	if err := s.ops.Create(ctx, op); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	s.workers.Enqueue(op)
	return op, nil
}

func (s *ImageService) handlePromote(ctx context.Context, op *domain.Operation) error {
	img, err := s.images.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	snap, err := s.snapshots.Get(ctx, img.SourceSnapshotID)
	if err != nil {
		return err
	}

	snapshotRef := domain.BackendRef{Backend: snap.Backend, Ref: snap.BackendRef}
	imgRef, err := s.provider.PublishSnapshotAsImage(ctx, snapshotRef, domain.PublishImageRequest{
		Name: img.Name, Version: img.Version,
	})
	if err != nil {
		img.State = domain.ImageFailed
		s.images.Update(ctx, img)
		return err
	}

	info, _ := s.provider.GetImageInfo(ctx, imgRef)
	img.BackendRef = imgRef.Ref
	img.Architecture = info.Architecture
	img.State = domain.ImageReady
	return s.images.Update(ctx, img)
}

func (s *ImageService) handleDelete(ctx context.Context, op *domain.Operation) error {
	img, err := s.images.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	if img.BackendRef != "" {
		imgRef := domain.BackendRef{Backend: img.Backend, Ref: img.BackendRef}
		if err := s.provider.DeleteImage(ctx, imgRef); err != nil {
			return err
		}
	}
	img.State = domain.ImageDeleted
	return s.images.Update(ctx, img)
}
