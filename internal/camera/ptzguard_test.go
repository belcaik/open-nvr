package camera

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

type call struct {
	ch  int
	cmd nvr.PTZCommand
	on  bool
}

type fakeMover struct {
	mu    sync.Mutex
	calls []call
	fail  int // número de fallos antes de responder bien
}

func (f *fakeMover) PTZ(_ context.Context, ch int, cmd nvr.PTZCommand, on bool, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{ch, cmd, on})
	if f.fail > 0 {
		f.fail--
		return errors.New("fallo")
	}
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup() (*Guard, *fakeMover, *clock) {
	m := &fakeMover{}
	c := &clock{t: time.Unix(1000, 0)}
	return NewGuard(m, 1500*time.Millisecond, c.now), m, c
}

func TestExpireStopsWithoutKeepalive(t *testing.T) {
	g, m, c := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnLeft, 5)
	c.t = c.t.Add(1400 * time.Millisecond)
	g.Expire(ctx)
	if len(m.calls) != 1 {
		t.Fatalf("paró antes de tiempo: %v", m.calls)
	}
	c.t = c.t.Add(200 * time.Millisecond)
	g.Expire(ctx)
	if got := m.calls[len(m.calls)-1]; got != (call{1, nvr.TurnLeft, false}) {
		t.Fatalf("última llamada = %v", got)
	}
	if g.Keepalive(1) {
		t.Fatal("sigue activo tras expirar")
	}
}

func TestKeepaliveExtends(t *testing.T) {
	g, m, c := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnUp, 5)
	for i := 0; i < 10; i++ {
		c.t = c.t.Add(500 * time.Millisecond)
		if !g.Keepalive(1) {
			t.Fatal("keepalive rechazado")
		}
		g.Expire(ctx)
	}
	if len(m.calls) != 1 {
		t.Fatalf("no debía parar: %v", m.calls)
	}
}

func TestStartReplacesActiveMotion(t *testing.T) {
	g, m, _ := setup()
	ctx := context.Background()
	g.Start(ctx, 1, nvr.TurnLeft, 5)
	g.Start(ctx, 1, nvr.TurnRight, 5)
	want := []call{{1, nvr.TurnLeft, true}, {1, nvr.TurnLeft, false}, {1, nvr.TurnRight, true}}
	if len(m.calls) != 3 || m.calls[0] != want[0] || m.calls[1] != want[1] || m.calls[2] != want[2] {
		t.Fatalf("llamadas = %v", m.calls)
	}
}

func TestStopRetriesThreeTimes(t *testing.T) {
	g, m, _ := setup()
	ctx := context.Background()
	g.Start(ctx, 2, nvr.ZoomIn, 5)
	m.fail = 2
	if err := g.Stop(ctx, 2); err != nil {
		t.Fatalf("Stop = %v", err)
	}
	if len(m.calls) != 4 { // start + 2 fallos + 1 éxito
		t.Fatalf("llamadas = %v", m.calls)
	}
}

func TestStopAllOnShutdown(t *testing.T) {
	g, m, _ := setup()
	ctx, cancel := context.WithCancel(context.Background())
	g.Start(context.Background(), 1, nvr.TurnLeft, 5)
	g.Start(context.Background(), 3, nvr.ZoomOut, 5)
	done := make(chan struct{})
	go func() { g.Run(ctx); close(done) }()
	cancel()
	<-done
	stops := 0
	for _, c := range m.calls {
		if !c.on {
			stops++
		}
	}
	if stops != 2 {
		t.Fatalf("stops = %d (%v)", stops, m.calls)
	}
}
