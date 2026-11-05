package events

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
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

func startDispatcher(t *testing.T, d *Dispatcher) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		d.RunReady(ctx, ready)
		close(done)
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Dispatcher.RunReady no se suscribió")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Dispatcher.RunReady no terminó")
		}
	})
	return cancel
}

func TestDispatcherDeliversOneThousandEventsInOrder(t *testing.T) {
	var bus Bus
	var mu sync.Mutex
	var got []int
	taken := make(chan struct{}, 1000)
	dispatcher := &Dispatcher{
		Bus: &bus,
		OnEvent: func(vision.Event) {
			taken <- struct{}{}
		},
		Sinks: []Sink{sinkFunc(func(_ context.Context, ev vision.Event) error {
			n, err := strconv.Atoi(ev.ID)
			if err != nil {
				t.Errorf("id = %q: %v", ev.ID, err)
				return nil
			}
			// Los envíos concurrentes terminarían fuera de orden: el más
			// reciente duerme menos, así el test falla si no hay cola.
			time.Sleep(time.Duration(1000-n) * time.Microsecond)
			mu.Lock()
			got = append(got, n)
			mu.Unlock()
			return nil
		})},
	}
	startDispatcher(t, dispatcher)
	const n = 1000
	const chunk = 32
	waitReceived := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			count := len(got)
			mu.Unlock()
			if count == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("recibidos = %d, quiero %d", count, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	for i := range n {
		bus.Publish(vision.Event{ID: strconv.Itoa(i), Kind: vision.EventZoneEnter, Cam: 1})
		select {
		case <-taken:
		case <-time.After(2 * time.Second):
			t.Fatalf("el dispatcher no tomó el evento %d", i)
		}
		if (i+1)%chunk == 0 {
			waitReceived(i + 1)
		}
	}
	waitReceived(n)
	mu.Lock()
	defer mu.Unlock()
	for i, v := range got {
		if v != i {
			t.Fatalf("orden: posición %d = %d, quiero %d", i, v, i)
		}
	}
}

func TestDispatcherSlowSinkDoesNotBlockAnother(t *testing.T) {
	var bus Bus
	block := make(chan struct{})
	fast := make(chan string, 8)
	dispatcher := &Dispatcher{
		Bus: &bus,
		Sinks: []Sink{
			sinkFunc(func(ctx context.Context, ev vision.Event) error {
				if ev.ID == "lento" {
					select {
					case <-block:
					case <-ctx.Done():
					}
				}
				return nil
			}),
			sinkFunc(func(_ context.Context, ev vision.Event) error {
				fast <- ev.ID
				return nil
			}),
		},
	}
	startDispatcher(t, dispatcher)
	bus.Publish(vision.Event{ID: "lento", Kind: vision.EventZoneEnter, Cam: 1})
	bus.Publish(vision.Event{ID: "rápido", Kind: vision.EventZoneLeave, Cam: 1})
	got := make([]string, 0, 2)
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case id := <-fast:
			got = append(got, id)
		case <-deadline:
			t.Fatalf("el sink lento bloqueó al otro; recibidos %v", got)
		}
	}
	close(block)
	if got[0] != "lento" || got[1] != "rápido" {
		t.Fatalf("el sink rápido recibió %v, quiero [lento rápido]", got)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDispatcherDropsWhenSinkQueueIsFull(t *testing.T) {
	var logs syncBuffer
	old := logx.Out
	logx.Out = &logs
	t.Cleanup(func() { logx.Out = old })

	var bus Bus
	started := make(chan struct{})
	block := make(chan struct{})
	var mu sync.Mutex
	var got []string
	var onEvent int
	dispatcher := &Dispatcher{
		Bus: &bus,
		OnEvent: func(vision.Event) {
			mu.Lock()
			onEvent++
			mu.Unlock()
		},
		Sinks: []Sink{sinkFunc(func(ctx context.Context, ev vision.Event) error {
			if ev.ID == "0" {
				close(started)
				select {
				case <-block:
				case <-ctx.Done():
				}
			}
			mu.Lock()
			got = append(got, ev.ID)
			mu.Unlock()
			return nil
		})},
	}
	startDispatcher(t, dispatcher)
	const queued = 64
	bus.Publish(vision.Event{ID: "0", Kind: vision.EventTrackingStart, Cam: 2})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("el sink no empezó a enviar")
	}
	for i := 1; i <= queued+1; i++ {
		bus.Publish(vision.Event{ID: strconv.Itoa(i), Kind: vision.EventTrackingStart, Cam: 2})
	}
	deadlineEvents := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := onEvent
		mu.Unlock()
		if n == queued+2 {
			break
		}
		if time.Now().After(deadlineEvents) {
			t.Fatalf("OnEvent = %d, quiero %d (síncrono antes de encolar)", n, queued+2)
		}
		time.Sleep(time.Millisecond)
	}
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(got)
		mu.Unlock()
		if count >= queued+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("entregados = %d, quiero %d", count, queued+1)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != queued+1 {
		t.Fatalf("entregados = %d, quiero %d (uno descartado)", len(got), queued+1)
	}
	if !strings.Contains(logs.String(), "cola de eventos llena") {
		t.Fatalf("no se registró el descarte: %q", logs.String())
	}
}

func TestDispatcherStopsSinkGoroutinesOnCancel(t *testing.T) {
	var bus Bus
	dispatcher := &Dispatcher{
		Bus: &bus,
		Sinks: []Sink{
			sinkFunc(func(context.Context, vision.Event) error { return nil }),
			sinkFunc(func(context.Context, vision.Event) error { return nil }),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		dispatcher.RunReady(ctx, ready)
		close(done)
	}()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("Dispatcher.RunReady no se suscribió")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("las goroutines del dispatcher no terminaron al cancelar")
	}
}
