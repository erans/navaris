package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type OperationHandler func(ctx context.Context, op *domain.Operation) error

type Dispatcher struct {
	opStore    domain.OperationStore
	events     domain.EventBus
	handlers   map[string]OperationHandler
	queue      chan *domain.Operation
	sem        chan struct{}
	cancels    sync.Map // operationID -> context.CancelFunc
	cancelled  sync.Map // operationID -> struct{} (pre-run cancellation)
	wg         sync.WaitGroup
	mu         sync.Mutex
	stopped    bool
	stopOnce   sync.Once
	done       chan struct{}
	opsTotal   metric.Int64Counter
}

func NewDispatcher(opStore domain.OperationStore, events domain.EventBus, concurrency int) *Dispatcher {
	if concurrency < 1 {
		concurrency = 1
	}
	d := &Dispatcher{
		opStore:  opStore,
		events:   events,
		handlers: make(map[string]OperationHandler),
		queue:    make(chan *domain.Operation, 256),
		sem:      make(chan struct{}, concurrency),
		done:     make(chan struct{}),
	}

	// Register telemetry instruments.
	meter := otel.Meter("navaris.dispatcher")

	queueDepth, _ := meter.Int64ObservableGauge("dispatcher.queue.depth",
		metric.WithDescription("Current number of queued operations"),
	)
	inflight, _ := meter.Int64ObservableGauge("dispatcher.inflight",
		metric.WithDescription("Currently executing operations"),
	)
	meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(queueDepth, int64(len(d.queue)))
		o.ObserveInt64(inflight, int64(len(d.sem)))
		return nil
	}, queueDepth, inflight)

	d.opsTotal, _ = meter.Int64Counter("dispatcher.operations.total",
		metric.WithDescription("Completed operations"),
	)

	return d
}

func (d *Dispatcher) Register(opType string, handler OperationHandler) {
	d.handlers[opType] = handler
}

func (d *Dispatcher) Start() {
	go d.loop()
}

func (d *Dispatcher) Stop() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopped = true
		d.mu.Unlock()

		close(d.done)

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
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		slog.Warn("dispatcher: enqueue after stop, dropping operation", "operation_id", op.OperationID)
		return
	}
	d.wg.Add(1)
	d.mu.Unlock()

	select {
	case d.queue <- op:
	case <-d.done:
		d.wg.Done()
		slog.Warn("dispatcher: enqueue during shutdown, dropping operation", "operation_id", op.OperationID)
	default:
		d.wg.Done()
		slog.Warn("dispatcher: queue full, dropping operation", "operation_id", op.OperationID)
	}
}

func (d *Dispatcher) Cancel(opID string) {
	// Cancel running operation
	if cancelFn, ok := d.cancels.LoadAndDelete(opID); ok {
		cancelFn.(context.CancelFunc)()
		return
	}
	// Mark for cancellation if still queued
	d.cancelled.Store(opID, struct{}{})
}

func (d *Dispatcher) WaitIdle() {
	d.wg.Wait()
}

func (d *Dispatcher) loop() {
	for {
		// Check done first to prevent starting new work after stop
		select {
		case <-d.done:
			d.drain()
			return
		default:
		}

		select {
		case <-d.done:
			d.drain()
			return
		case op := <-d.queue:
			d.sem <- struct{}{} // acquire
			go d.run(op)
		}
	}
}

func (d *Dispatcher) drain() {
	for {
		select {
		case <-d.queue:
			d.wg.Done()
		default:
			return
		}
	}
}

func (d *Dispatcher) run(op *domain.Operation) {
	defer func() {
		<-d.sem // release
		d.wg.Done()
	}()

	// Check if cancelled while queued
	if _, wasCancelled := d.cancelled.LoadAndDelete(op.OperationID); wasCancelled {
		now := time.Now().UTC()
		op.State = domain.OpCancelled
		op.FinishedAt = &now
		if err := d.opStore.Update(context.Background(), op); err != nil {
			slog.Error("dispatcher: failed to update op to cancelled", "error", err)
		}
		d.publishOpEvent(context.Background(), op)
		d.opsTotal.Add(context.Background(), 1,
			metric.WithAttributes(
				attribute.String("type", op.Type),
				attribute.String("status", string(op.State)),
			),
		)
		return
	}

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
		if errors.Is(err, context.Canceled) {
			d.cancel(op)
		} else {
			d.fail(op, err.Error())
		}
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
	d.opsTotal.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("type", op.Type),
			attribute.String("status", string(op.State)),
		),
	)
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
	d.opsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("type", op.Type),
			attribute.String("status", string(op.State)),
		),
	)
}

func (d *Dispatcher) cancel(op *domain.Operation) {
	now := time.Now().UTC()
	op.State = domain.OpCancelled
	op.FinishedAt = &now
	if err := d.opStore.Update(context.Background(), op); err != nil {
		slog.Error("dispatcher: failed to update op to cancelled", "error", err)
	}
	d.publishOpEvent(context.Background(), op)
	d.opsTotal.Add(context.Background(), 1,
		metric.WithAttributes(
			attribute.String("type", op.Type),
			attribute.String("status", string(op.State)),
		),
	)
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
