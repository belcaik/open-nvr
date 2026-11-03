package events

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/vision"
)

type sinkFunc func(ctx context.Context, ev vision.Event) error

func (f sinkFunc) Send(ctx context.Context, ev vision.Event) error {
	return f(ctx, ev)
}

func TestNtfySinkFiltersAndThrottlesByCameraZoneAndKind(t *testing.T) {
	var mu sync.Mutex
	var payloads []notify.Payload
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	sink := &NtfySink{
		Notifier: &notify.Notifier{
			URL: "https://ntfy.example", Topic: "vision",
			Throttle: notify.NewThrottle(&notify.ThrottleState{}, 60, 100),
			Send: func(_ string, payload notify.Payload, _ string) error {
				mu.Lock()
				payloads = append(payloads, payload)
				mu.Unlock()
				return nil
			},
		},
		Now: func() time.Time { return now },
	}
	enter := vision.Event{Kind: vision.EventZoneEnter, Cam: 1, ZoneID: "Puerta"}
	if err := sink.Send(context.Background(), enter); err != nil {
		t.Fatal(err)
	}
	if err := sink.Send(context.Background(), vision.Event{Kind: vision.EventZoneLeave, Cam: 1, ZoneID: "Puerta"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if err := sink.Send(context.Background(), enter); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(payloads) != 1 {
		mu.Unlock()
		t.Fatalf("envíos = %d, quiero 1", len(payloads))
	}
	if payloads[0].Title != "Persona en Puerta (cámara 1)" {
		mu.Unlock()
		t.Fatalf("título = %q", payloads[0].Title)
	}
	mu.Unlock()

	now = now.Add(30 * time.Second)
	if err := sink.Send(context.Background(), enter); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatalf("envíos tras el enfriamiento = %d, quiero 2", len(payloads))
	}
	if !strings.Contains(payloads[1].Message, "1 avisos anteriores suprimidos") {
		t.Fatalf("mensaje = %q", payloads[1].Message)
	}
}

func TestDispatcherSinkFailureDoesNotBlockHistoryOrOtherSinks(t *testing.T) {
	var bus Bus
	history := &History{Dir: t.TempDir()}
	received := make(chan vision.Event, 1)
	dispatcher := &Dispatcher{
		Bus: &bus, History: history,
		Sinks: []Sink{
			sinkFunc(func(context.Context, vision.Event) error { return errors.New("sink caído") }),
			sinkFunc(func(_ context.Context, ev vision.Event) error {
				received <- ev
				return nil
			}),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		dispatcher.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		bus.mu.RLock()
		subscribed := len(bus.subs) == 1
		bus.mu.RUnlock()
		if subscribed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Dispatcher.Run no se suscribió")
		}
		time.Sleep(time.Millisecond)
	}
	ev := vision.Event{ID: "dispatcher", Kind: vision.EventZoneLeave, Cam: 3,
		At: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	bus.Publish(ev)
	select {
	case got := <-received:
		if got.ID != ev.ID {
			t.Fatalf("evento = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el fallo de un sink impidió llegar al siguiente")
	}
	stored, err := history.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != ev.ID {
		t.Fatalf("historial = %+v", stored)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatcher.Run no terminó")
	}
}
