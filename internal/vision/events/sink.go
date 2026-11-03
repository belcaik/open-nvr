package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/vision"
)

// Sink recibe eventos para entregarlos a un sistema externo.
type Sink interface {
	Send(ctx context.Context, ev vision.Event) error
}

// NtfySink adapta el notificador ntfy a los eventos de visión.
type NtfySink struct {
	Notifier *notify.Notifier
	Now      func() time.Time

	mu sync.Mutex
}

// Send envía solo los avisos relevantes y limita repeticiones a uno por minuto.
func (s *NtfySink) Send(_ context.Context, ev vision.Event) error {
	var title, message string
	var priority int
	switch ev.Kind {
	case vision.EventZoneEnter:
		zone := ev.ZoneID
		if zone == "" {
			zone = "zona desconocida"
		}
		title = fmt.Sprintf("Persona en %s (cámara %d)", zone, ev.Cam)
		message = fmt.Sprintf("Se detectó una entrada en %s (cámara %d)", zone, ev.Cam)
		priority = notify.PriorityDefault
	case vision.EventCameraDisplaced:
		title = fmt.Sprintf("Cámara %d desplazada", ev.Cam)
		message = fmt.Sprintf("La cámara %d no está en su preset", ev.Cam)
		priority = notify.PriorityHigh
	case vision.EventDetectorDown:
		title = "Detector caído"
		message = "El motor de visión no está disponible"
		priority = notify.PriorityHigh
	default:
		return nil
	}
	if s.Notifier == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Notifier.Throttle == nil {
		s.Notifier.Throttle = notify.NewThrottle(&notify.ThrottleState{}, 60, 100)
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	key := fmt.Sprintf("%d/%s/%s", ev.Cam, ev.ZoneID, ev.Kind)
	s.Notifier.Notify(key, title, message, priority, nil, 60, float64(now.UnixNano())/1e9)
	return nil
}

// Dispatcher conserva cada evento y lo distribuye a los sinks.
type Dispatcher struct {
	Bus     *Bus
	History *History
	Sinks   []Sink
}

// Run procesa eventos hasta que termine el contexto.
func (d *Dispatcher) Run(ctx context.Context) {
	if d.Bus == nil {
		return
	}
	events, unsubscribe := d.Bus.Subscribe(64)
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if d.History != nil {
				// Los sinks reciben el evento ya guardado, con su instantánea.
				saved, err := d.History.Record(ctx, ev)
				if err != nil {
					logx.Printf("no se pudo guardar evento de visión: %v", err)
				} else {
					ev = saved
				}
			}
			for _, sink := range d.Sinks {
				go d.send(ctx, sink, ev)
			}
		}
	}
}

func (d *Dispatcher) send(ctx context.Context, sink Sink, ev vision.Event) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sink.Send(ctx, ev); err != nil {
		logx.Printf("error enviando evento de visión: %v", err)
	}
}
