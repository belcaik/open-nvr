package events

import (
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/vision"
)

func TestPublishDoesNotBlockAndCountsDrops(t *testing.T) {
	var bus Bus
	ch, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()

	bus.Publish(vision.Event{ID: "primero"})
	done := make(chan struct{})
	go func() {
		bus.Publish(vision.Event{ID: "descartado"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish se bloqueó con el buffer lleno")
	}
	if got := bus.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d, quiero 1", got)
	}
	if ev := <-ch; ev.ID != "primero" {
		t.Fatalf("evento recibido = %q", ev.ID)
	}
	unsubscribe()
	unsubscribe()
	if _, ok := <-ch; ok {
		t.Fatal("el canal debía quedar cerrado tras la baja")
	}
}
