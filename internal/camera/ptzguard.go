// Package camera une el cliente del NVR con las reglas de la interfaz.
package camera

import (
	"context"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/nvr"
)

// Mover es la parte del cliente del NVR que mueve la cámara.
type Mover interface {
	PTZ(ctx context.Context, ch int, cmd nvr.PTZCommand, on bool, speed int) error
}

type motion struct {
	cmd      nvr.PTZCommand
	speed    int
	deadline time.Time
}

// Guard garantiza que ningún movimiento continuo se quede activo: si el
// navegador deja de enviar keepalives (red caída, pestaña cerrada,
// proceso parado), envía el stop por su cuenta.
type Guard struct {
	mv      Mover
	timeout time.Duration
	now     func() time.Time
	mu      sync.Mutex
	active  map[int]motion
}

func NewGuard(mv Mover, timeout time.Duration, now func() time.Time) *Guard {
	return &Guard{mv: mv, timeout: timeout, now: now, active: map[int]motion{}}
}

// Start detiene antes cualquier movimiento activo en ch y arranca cmd.
func (g *Guard) Start(ctx context.Context, ch int, cmd nvr.PTZCommand, speed int) error {
	if err := g.Stop(ctx, ch); err != nil {
		return err
	}
	g.mu.Lock()
	g.active[ch] = motion{cmd: cmd, speed: speed, deadline: g.now().Add(g.timeout)}
	g.mu.Unlock()
	if err := g.mv.PTZ(ctx, ch, cmd, true, speed); err != nil {
		// Puede que el NVR haya arrancado igualmente: se intenta parar.
		g.Stop(context.WithoutCancel(ctx), ch)
		return err
	}
	return nil
}

// Keepalive prolonga el plazo; false si no hay movimiento activo en ch.
func (g *Guard) Keepalive(ch int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.active[ch]
	if ok {
		m.deadline = g.now().Add(g.timeout)
		g.active[ch] = m
	}
	return ok
}

// Stop envía el stop del comando activo (hasta 3 intentos) y lo olvida.
// Sin movimiento activo no hace nada.
func (g *Guard) Stop(ctx context.Context, ch int) error {
	g.mu.Lock()
	m, ok := g.active[ch]
	delete(g.active, ch)
	g.mu.Unlock()
	if !ok {
		return nil
	}
	return g.sendStop(ctx, ch, m)
}

func (g *Guard) sendStop(ctx context.Context, ch int, m motion) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = g.mv.PTZ(ctx, ch, m.cmd, false, m.speed); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	logx.Printf("PTZ canal %d: no se pudo parar %s: %v", ch, m.cmd, err)
	return err
}

// Expire detiene los movimientos cuyo plazo ya venció.
func (g *Guard) Expire(ctx context.Context) {
	now := g.now()
	g.mu.Lock()
	expired := map[int]motion{}
	for ch, m := range g.active {
		if !now.Before(m.deadline) {
			expired[ch] = m
			delete(g.active, ch)
		}
	}
	g.mu.Unlock()
	for ch, m := range expired {
		logx.Printf("PTZ canal %d: sin keepalive, stop de seguridad de %s", ch, m.cmd)
		g.sendStop(ctx, ch, m)
	}
}

// Run revisa los plazos cada 250 ms; al cancelar ctx detiene todo.
func (g *Guard) Run(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			g.StopAll(context.Background())
			return
		case <-t.C:
			g.Expire(ctx)
		}
	}
}

// StopAll detiene todos los movimientos activos (apagado del proceso).
func (g *Guard) StopAll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	g.mu.Lock()
	all := g.active
	g.active = map[int]motion{}
	g.mu.Unlock()
	for ch, m := range all {
		g.sendStop(ctx, ch, m)
	}
}
