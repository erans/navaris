package service

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/worker"
)

type OperationService struct {
	ops     domain.OperationStore
	workers *worker.Dispatcher
}

func NewOperationService(ops domain.OperationStore, workers *worker.Dispatcher) *OperationService {
	return &OperationService{ops: ops, workers: workers}
}

func (s *OperationService) Get(ctx context.Context, id string) (*domain.Operation, error) {
	return s.ops.Get(ctx, id)
}

func (s *OperationService) List(ctx context.Context, filter domain.OperationFilter) ([]*domain.Operation, error) {
	return s.ops.List(ctx, filter)
}

func (s *OperationService) Cancel(ctx context.Context, id string) error {
	op, err := s.ops.Get(ctx, id)
	if err != nil {
		return err
	}
	if op.State.Terminal() {
		return nil // already finished
	}
	s.workers.Cancel(id)
	return nil
}
