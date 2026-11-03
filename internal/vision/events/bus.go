// Package events distribuye y conserva los eventos del motor de visión.
package events

import (
	"sync"
	"sync/atomic"

	"github.com/belcaik/open-nvr/internal/vision"
)

// Bus reparte eventos sin bloquear al publicador.
type Bus struct {
	mu      sync.RWMutex
	nextID  uint64
	subs    map[uint64]chan vision.Event
	dropped atomic.Uint64
}

// Publish entrega el evento a cada suscriptor con espacio disponible.
func (b *Bus) Publish(ev vision.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Subscribe añade un suscriptor y devuelve su función de baja idempotente.
func (b *Bus) Subscribe(buffer int) (<-chan vision.Event, func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan vision.Event, buffer)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = make(map[uint64]chan vision.Event)
	}
	b.nextID++
	id := b.nextID
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(ch)
			}
			b.mu.Unlock()
		})
	}
	return ch, unsubscribe
}

// Dropped devuelve cuántas entregas se descartaron por buffers llenos.
func (b *Bus) Dropped() uint64 {
	return b.dropped.Load()
}
