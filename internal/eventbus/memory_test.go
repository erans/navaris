package eventbus_test

import (
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/eventbus"
)

func TestPublishSubscribe(t *testing.T) {
	bus := eventbus.New(64)
	ch, cancel, err := bus.Subscribe(t.Context(), domain.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	evt := domain.Event{
		Type:      domain.EventSandboxStateChanged,
		Timestamp: time.Now().UTC(),
		Data:      map[string]any{"sandbox_id": "s1"},
	}
	bus.Publish(t.Context(), evt)

	select {
	case got := <-ch:
		if got.Type != domain.EventSandboxStateChanged {
			t.Errorf("got type %s, want %s", got.Type, domain.EventSandboxStateChanged)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSubscribeWithFilter(t *testing.T) {
	bus := eventbus.New(64)
	sid := "sandbox-123"
	ch, cancel, err := bus.Subscribe(t.Context(), domain.EventFilter{SandboxID: &sid})
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	// Event for different sandbox — should not be received
	bus.Publish(t.Context(), domain.Event{
		Type: domain.EventSandboxStateChanged,
		Data: map[string]any{"sandbox_id": "other"},
	})
	// Event for matching sandbox — should be received
	bus.Publish(t.Context(), domain.Event{
		Type: domain.EventSandboxStateChanged,
		Data: map[string]any{"sandbox_id": sid},
	})

	select {
	case got := <-ch:
		if got.Data["sandbox_id"] != sid {
			t.Errorf("got wrong sandbox_id: %v", got.Data["sandbox_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	bus := eventbus.New(64)
	ch, cancel, _ := bus.Subscribe(t.Context(), domain.EventFilter{})
	cancel()

	bus.Publish(t.Context(), domain.Event{Type: domain.EventSandboxStateChanged})
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after cancel")
		}
	case <-time.After(100 * time.Millisecond):
		// Also acceptable — no event delivered
	}
}
