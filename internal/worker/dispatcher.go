package worker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/navaris/navaris/internal/domain"
)

type OperationHandler func(ctx context.Context, op *domain.Operation) error

type Dispatcher struct {
	opStore  domain.OperationStore
	events   domain.EventBus
	handlers map[string]OperationHandler
	queue    chan *domain.Operation
	sem      chan struct{}
	cancels  sync.Map // operationID -> context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopped  atomic.Bool
	done     chan struct{}
}

func NewDispatcher(opStore domain.OperationStore, events domain.EventBus, concurrency int) *Dispatcher {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Dispatcher{
		opStore:  opStore,
		events:   events,
		handlers: make(map[string]OperationHandler),
		queue:    make(chan *domain.Operation, 256),
		sem:      make(chan struct{}, concurrency),
		done:     make(chan struct{}),
	}
}

func (d *Dispatcher) Register(opType string, handler OperationHandler) {
	d.handlers[opType] = handler
}

func (d *Dispatcher) Start() {
	go d.loop()
}

func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		d.stopped.Store(true)
		close(d.done)
		// Drain remaining queued items so wg accounting balances
		go func() {
			for range d.queue {
				d.wg.Done()
			}
		}()
		close(d.queue)
		// Wait for in-flight handlers with timeout
		ch := make(chan struct{})
		go func() {
			d.wg.Wait()
			close(ch)
		}()
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			slog.Warn("dispatcher: timeout waiting for in-flight operations")
		}
	})
}

func (d *Dispatcher) Enqueue(op *domain.Operation) {
	if d.stopped.Load() {
		slog.Warn("dispatcher: enqueue after stop, dropping operation", "operation_id", op.OperationID)
		return
	}
	d.wg.Add(1)
	select {
	case d.queue <- op:
	default:
		d.wg.Done()
		slog.Warn("dispatcher: queue full, dropping operation", "operation_id", op.OperationID)
	}
}

func (d *Dispatcher) Cancel(opID string) {
	if cancelFn, ok := d.cancels.LoadAndDelete(opID); ok {
		cancelFn.(context.CancelFunc)()
	}
}

func (d *Dispatcher) WaitIdle() {
	d.wg.Wait()
}

func (d *Dispatcher) loop() {
	for {
		select {
		case <-d.done:
			return
		case op := <-d.queue:
			d.sem <- struct{}{} // acquire
			go d.run(op)
		}
	}
}

func (d *Dispatcher) run(op *domain.Operation) {
	defer func() {
		<-d.sem // release
		d.wg.Done()
	}()

	handler, ok := d.handlers[op.Type]
	if !ok {
		slog.Error("dispatcher: no handler registered", "type", op.Type)
		d.fail(op, "no handler registered for operation type: "+op.Type)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.cancels.Store(op.OperationID, cancel)
	defer func() {
		d.cancels.Delete(op.OperationID)
		cancel()
	}()

	// Mark as running
	op.State = domain.OpRunning
	if err := d.opStore.Update(ctx, op); err != nil {
		slog.Error("dispatcher: failed to update op to running", "error", err)
	}
	d.publishOpEvent(ctx, op)

	// Execute handler
	if err := handler(ctx, op); err != nil {
		d.fail(op, err.Error())
		return
	}

	// Mark as succeeded
	now := time.Now().UTC()
	op.State = domain.OpSucceeded
	op.FinishedAt = &now
	if err := d.opStore.Update(ctx, op); err != nil {
		slog.Error("dispatcher: failed to update op to succeeded", "error", err)
	}
	d.publishOpEvent(ctx, op)
}

func (d *Dispatcher) fail(op *domain.Operation, errText string) {
	now := time.Now().UTC()
	op.State = domain.OpFailed
	op.FinishedAt = &now
	op.ErrorText = errText
	if err := d.opStore.Update(context.Background(), op); err != nil {
		slog.Error("dispatcher: failed to update op to failed", "error", err)
	}
	d.publishOpEvent(context.Background(), op)
}

func (d *Dispatcher) publishOpEvent(ctx context.Context, op *domain.Operation) {
	d.events.Publish(ctx, domain.Event{
		Type:      domain.EventOperationStateChanged,
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"operation_id": op.OperationID,
			"state":        string(op.State),
			"type":         op.Type,
		},
	})
}
