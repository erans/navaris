package worker_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
	"github.com/navaris/navaris/internal/worker"
)

type mockOpStore struct {
	mu  sync.Mutex
	ops map[string]*domain.Operation
}

func (m *mockOpStore) Create(_ context.Context, op *domain.Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops[op.OperationID] = op
	return nil
}

func (m *mockOpStore) Get(_ context.Context, id string) (*domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.ops[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return op, nil
}

func (m *mockOpStore) Update(_ context.Context, op *domain.Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops[op.OperationID] = op
	return nil
}

func (m *mockOpStore) List(_ context.Context, _ domain.OperationFilter) ([]*domain.Operation, error) {
	return nil, nil
}

func (m *mockOpStore) ListStale(_ context.Context, _ time.Time) ([]*domain.Operation, error) {
	return nil, nil
}

func (m *mockOpStore) ListByState(_ context.Context, _ domain.OperationState) ([]*domain.Operation, error) {
	return nil, nil
}

func TestDispatcherRunsHandler(t *testing.T) {
	store := &mockOpStore{ops: make(map[string]*domain.Operation)}
	bus := eventbus.New(64)
	d := worker.NewDispatcher(store, bus, 4)

	var called atomic.Bool
	d.Register("test_op", func(ctx context.Context, op *domain.Operation) error {
		called.Store(true)
		return nil
	})
	d.Start()
	defer d.Stop()

	op := &domain.Operation{
		OperationID: uuid.NewString(),
		Type:        "test_op",
		State:       domain.OpPending,
		StartedAt:   time.Now().UTC(),
	}
	store.ops[op.OperationID] = op
	d.Enqueue(op)

	d.WaitIdle()
	if !called.Load() {
		t.Error("handler was not called")
	}
	store.mu.Lock()
	if store.ops[op.OperationID].State != domain.OpSucceeded {
		t.Errorf("expected state Succeeded, got %s", store.ops[op.OperationID].State)
	}
	store.mu.Unlock()
}

func TestDispatcherHandlerError(t *testing.T) {
	store := &mockOpStore{ops: make(map[string]*domain.Operation)}
	bus := eventbus.New(64)
	d := worker.NewDispatcher(store, bus, 4)

	d.Register("fail_op", func(ctx context.Context, op *domain.Operation) error {
		return fmt.Errorf("something broke")
	})
	d.Start()
	defer d.Stop()

	op := &domain.Operation{
		OperationID: uuid.NewString(),
		Type:        "fail_op",
		State:       domain.OpPending,
		StartedAt:   time.Now().UTC(),
	}
	store.ops[op.OperationID] = op
	d.Enqueue(op)

	d.WaitIdle()
	store.mu.Lock()
	if store.ops[op.OperationID].State != domain.OpFailed {
		t.Errorf("expected state Failed, got %s", store.ops[op.OperationID].State)
	}
	if store.ops[op.OperationID].ErrorText == "" {
		t.Error("expected error_text to be set")
	}
	store.mu.Unlock()
}

func TestDispatcherConcurrencyLimit(t *testing.T) {
	store := &mockOpStore{ops: make(map[string]*domain.Operation)}
	bus := eventbus.New(64)
	d := worker.NewDispatcher(store, bus, 2) // limit = 2

	var running atomic.Int32
	var maxSeen atomic.Int32
	gate := make(chan struct{})

	d.Register("slow", func(ctx context.Context, op *domain.Operation) error {
		cur := running.Add(1)
		for {
			old := maxSeen.Load()
			if cur <= old || maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}
		<-gate
		running.Add(-1)
		return nil
	})
	d.Start()
	defer d.Stop()

	for i := 0; i < 4; i++ {
		op := &domain.Operation{
			OperationID: fmt.Sprintf("op-%d", i),
			Type:        "slow",
			State:       domain.OpPending,
			StartedAt:   time.Now().UTC(),
		}
		store.mu.Lock()
		store.ops[op.OperationID] = op
		store.mu.Unlock()
		d.Enqueue(op)
	}

	time.Sleep(50 * time.Millisecond)
	close(gate)
	d.WaitIdle()

	if maxSeen.Load() > 2 {
		t.Fatalf("expected max 2 concurrent, saw %d", maxSeen.Load())
	}
}
