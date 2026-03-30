package service

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

type OperationService struct {
	ops     domain.OperationStore
	workers *worker.Dispatcher
}

func NewOperationService(ops domain.OperationStore, workers *worker.Dispatcher) *OperationService {
	return &OperationService{ops: ops, workers: workers}
}

func (s *OperationService) Get(ctx context.Context, id string) (*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.GetOperation")
	defer span.End()

	op, err := s.ops.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return op, nil
}

func (s *OperationService) List(ctx context.Context, filter domain.OperationFilter) ([]*domain.Operation, error) {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.ListOperations")
	defer span.End()

	list, err := s.ops.List(ctx, filter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return list, nil
}

func (s *OperationService) Cancel(ctx context.Context, id string) error {
	ctx, span := otel.Tracer("navaris.service").Start(ctx, "service.CancelOperation")
	defer span.End()

	op, err := s.ops.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if op.State.Terminal() {
		return nil // already finished
	}
	s.workers.Cancel(id)
	return nil
}
