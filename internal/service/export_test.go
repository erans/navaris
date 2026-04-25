package service

import (
	"context"

	"github.com/navaris/navaris/internal/domain"
)

// HandleFork exposes the unexported handleFork method for white-box tests in
// the service_test package.
func (s *SandboxService) HandleFork(ctx context.Context, op *domain.Operation) error {
	return s.handleFork(ctx, op)
}
