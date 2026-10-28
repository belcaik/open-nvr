package camera

import (
	"context"
	"sync"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/nvr"
)

// EventSource es nvr.Client.Events (inyectable para tests).
type EventSource func(ctx context.Context, channels []int) (<-chan nvr.Event, <-chan error)

// Hub mantiene una única suscripción a los eventos del NVR y la reparte a
// todos los navegadores conectados. Un suscriptor lento pierde eventos en
// lugar de frenar a los demás.
type Hub struct {
	mu   sync.Mutex
	subs map[chan nvr.Event]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan nvr.Event]struct{}{}} }

// Run lee de src hasta que ctx termina.
func (h *Hub) Run(ctx context.Context, src EventSource, channels []int) {
	events, errs := src(ctx, channels)
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errs:
			if ok && err != nil {
				logx.Printf("eventos del NVR: %v", err)
			}
			if !ok {
				errs = nil
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Kind == "unknown" {
				// Formato aún sin documentar: se registra para aprenderlo.
				logx.Printf("evento del NVR sin clasificar: %s", ev.Raw)
			}
			h.publish(ev)
		}
	}
}

func (h *Hub) publish(ev nvr.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe devuelve un canal de eventos y la función para darse de baja.
func (h *Hub) Subscribe() (<-chan nvr.Event, func()) {
	ch := make(chan nvr.Event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// Subscribers solo para tests y diagnóstico.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
