package domain

import "context"

type EventBus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(ctx context.Context, filter EventFilter) (<-chan Event, func(), error)
}
