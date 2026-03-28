package eventbus

import (
	"context"
	"sync"

	"github.com/navaris/navaris/internal/domain"
)

var _ domain.EventBus = (*MemoryBus)(nil)

type subscriber struct {
	ch     chan domain.Event
	filter domain.EventFilter
}

type MemoryBus struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
	bufSize     int
}

func New(bufSize int) *MemoryBus {
	return &MemoryBus{
		subscribers: make(map[*subscriber]struct{}),
		bufSize:     bufSize,
	}
}

func (b *MemoryBus) Publish(_ context.Context, event domain.Event) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subscribers {
		if matches(sub.filter, event) {
			select {
			case sub.ch <- event:
			default:
				// drop if full
			}
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(_ context.Context, filter domain.EventFilter) (<-chan domain.Event, func(), error) {
	ch := make(chan domain.Event, b.bufSize)
	sub := &subscriber{ch: ch, filter: filter}

	b.mu.Lock()
	b.subscribers[sub] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		delete(b.subscribers, sub)
		b.mu.Unlock()
		close(ch)
	}
	return ch, cancel, nil
}

func matches(filter domain.EventFilter, event domain.Event) bool {
	if filter.SandboxID != nil {
		sid, _ := event.Data["sandbox_id"].(string)
		if sid != *filter.SandboxID {
			return false
		}
	}
	if filter.ProjectID != nil {
		pid, _ := event.Data["project_id"].(string)
		if pid != *filter.ProjectID {
			return false
		}
	}
	if len(filter.Types) > 0 {
		found := false
		for _, t := range filter.Types {
			if t == event.Type {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
