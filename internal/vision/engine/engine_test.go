package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/camera"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/vision"
)

var _ PTZ = (*camera.Service)(nil)

type fakePTZ struct {
	mu        sync.Mutex
	starts    []nvr.PTZCommand
	stops     int
	presets   []int
	startErr  error
	presetErr error
	started   chan struct{}
}

func (f *fakePTZ) PTZStart(_ context.Context, _ int, cmd nvr.PTZCommand, _ int) error {
	f.mu.Lock()
	f.starts = append(f.starts, cmd)
	started := f.started
	err := f.startErr
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	return err
}

func (f *fakePTZ) PTZStop(context.Context, int) error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	return nil
}

func (f *fakePTZ) Preset(_ context.Context, _ int, _ nvr.PresetAction, idx int) error {
	f.mu.Lock()
	f.presets = append(f.presets, idx)
	err := f.presetErr
	f.mu.Unlock()
	return err
}

func (f *fakePTZ) snapshot() ([]nvr.PTZCommand, int, []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nvr.PTZCommand(nil), f.starts...), f.stops, append([]int(nil), f.presets...)
}

type fakeVerifier struct {
	mu     sync.Mutex
	checks []Check
	calls  int
}

func (v *fakeVerifier) Verify(context.Context, int, int) (Check, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	if len(v.checks) == 0 {
		return Check{}, nil
	}
	check := v.checks[0]
	v.checks = v.checks[1:]
	return check, nil
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func baseConfig() vision.CameraConfig {
	return vision.CameraConfig{
		Enabled:    true,
		HomePreset: 2,
		Tracking: vision.TrackingConfig{
			Enabled:          true,
			Speed:            5,
			ZoomTargetHeight: 0.5,
			ReturnAfter:      5 * time.Second,
			MaxPanPulses:     5,
			MaxTiltPulses:    5,
			MaxZoomPulses:    5,
		},
	}
}

func newTestEngine(cfg *vision.CameraConfig) (*Engine, *fakePTZ, *testClock) {
	ptz := &fakePTZ{}
	clock := &testClock{now: time.Unix(100, 0)}
	e := &Engine{
		Cam: 1,
		Cfg: func() vision.CameraConfig { return *cfg },
		PTZ: ptz,
		Clock: Clock{
			Now: clock.Now,
			Sleep: func(context.Context, time.Duration) error {
				return nil
			},
		},
		SettleDelay: time.Millisecond,
	}
	e.init()
	e.finishReturn(Pose{Preset: cfg.HomePreset, Verified: true})
	return e, ptz, clock
}

func track(id int, conf, x1, y1, x2, y2 float64) vision.Track {
	return vision.Track{ID: id, Conf: conf, Box: vision.Box{x1, y1, x2, y2}}
}

func frame(tracks ...vision.Track) vision.Frame {
	return vision.Frame{Cam: 1, Tracks: tracks}
}

func TestPersonaCentradaNoMueve(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	if err := e.processFrame(context.Background(), frame(track(1, .9, .45, .45, .55, .65))); err != nil {
		t.Fatal(err)
	}
	starts, stops, _ := ptz.snapshot()
	if len(starts) != 0 || stops != 0 {
		t.Fatalf("movimientos = %v, stops = %d; quería ninguno", starts, stops)
	}
}

func TestPersonaALaIzquierdaEmiteTurnLeft(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
	}
	starts, stops, _ := ptz.snapshot()
	if len(starts) != 1 || starts[0] != nvr.TurnLeft || stops != 1 {
		t.Fatalf("starts = %v, stops = %d", starts, stops)
	}
}

func TestPersonaArribaALaDerechaEmiteDiagonal(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .9, .75, .1, .9, .3)))
	}
	starts, _, _ := ptz.snapshot()
	if len(starts) != 1 || starts[0] != nvr.TurnUpRight {
		t.Fatalf("starts = %v", starts)
	}
}

func TestTodoStartTieneStop(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		cfg := baseConfig()
		e, ptz, _ := newTestEngine(&cfg)
		for i := 0; i < 3; i++ {
			_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
		}
		starts, stops, _ := ptz.snapshot()
		if len(starts) != stops {
			t.Fatalf("starts = %d, stops = %d", len(starts), stops)
		}
	})
	t.Run("falla Start", func(t *testing.T) {
		cfg := baseConfig()
		e, ptz, _ := newTestEngine(&cfg)
		ptz.startErr = errors.New("falló")
		for i := 0; i < 3; i++ {
			_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
		}
		starts, stops, _ := ptz.snapshot()
		if len(starts) != 1 || stops != 1 {
			t.Fatalf("starts = %d, stops = %d", len(starts), stops)
		}
	})
	t.Run("contexto cancelado a mitad", func(t *testing.T) {
		cfg := baseConfig()
		e, ptz, _ := newTestEngine(&cfg)
		ptz.started = make(chan struct{}, 1)
		e.Clock.Sleep = func(ctx context.Context, _ time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		for i := 0; i < 2; i++ {
			_ = e.processFrame(ctx, frame(track(1, .9, .1, .45, .2, .65)))
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = e.processFrame(ctx, frame(track(1, .9, .1, .45, .2, .65)))
		}()
		<-ptz.started
		cancel()
		<-done
		starts, stops, _ := ptz.snapshot()
		if len(starts) != 1 || stops != 1 {
			t.Fatalf("starts = %d, stops = %d", len(starts), stops)
		}
	})
}

func TestLimitePanPermiteVolverEnSentidoContrario(t *testing.T) {
	cfg := baseConfig()
	cfg.Tracking.MaxPanPulses = 1
	e, ptz, _ := newTestEngine(&cfg)
	left := frame(track(1, .9, .1, .45, .2, .65))
	right := frame(track(1, .9, .8, .45, .9, .65))
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), left)
	}
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), left)
	}
	_ = e.processFrame(context.Background(), left)
	_ = e.processFrame(context.Background(), right)
	starts, _, _ := ptz.snapshot()
	if len(starts) != 2 || starts[0] != nvr.TurnLeft || starts[1] != nvr.TurnRight {
		t.Fatalf("starts = %v", starts)
	}
}

func TestIgnoraTresFramesTrasPulso(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	left := frame(track(1, .9, .1, .45, .2, .65))
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), left)
	}
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), left)
		starts, _, _ := ptz.snapshot()
		if len(starts) != 1 {
			t.Fatalf("frame ignorado %d produjo %v", i+1, starts)
		}
	}
	_ = e.processFrame(context.Background(), left)
	starts, _, _ := ptz.snapshot()
	if len(starts) != 2 {
		t.Fatalf("cuarto frame: starts = %v", starts)
	}
}

func fullZone(id string, preset int, zoom bool) vision.Zone {
	return vision.Zone{ID: id, Pose: vision.Pose{Preset: preset}, Zoom: zoom, Points: []vision.Point{
		{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}, {X: 0, Y: 1},
	}}
}

func TestZoneAlertFiresBeforeTracking(t *testing.T) {
	cfg := baseConfig()
	cfg.Zones = []vision.Zone{{
		ID: "door", Pose: vision.Pose{Preset: 1}, Points: []vision.Point{
			{X: .65, Y: .3}, {X: .85, Y: .3}, {X: .85, Y: .8}, {X: .65, Y: .8},
		},
	}}
	cfg.HomePreset = 1
	e, ptz, _ := newTestEngine(&cfg)
	var events []vision.Event
	startsAtEnter := -1
	e.Emit = func(event vision.Event) {
		events = append(events, event)
		if event.Kind == vision.EventZoneEnter {
			starts, _, _ := ptz.snapshot()
			startsAtEnter = len(starts)
		}
	}

	for i := 0; i < 3; i++ {
		if err := e.processFrame(context.Background(), frame(track(7, .9, .45, .4, .55, .7))); err != nil {
			t.Fatal(err)
		}
	}
	if starts, _, _ := ptz.snapshot(); len(starts) != 0 {
		t.Fatalf("PTZ antes de entrar en zona = %v", starts)
	}
	for i := 0; i < 3; i++ {
		if err := e.processFrame(context.Background(), frame(track(7, .9, .68, .4, .78, .7))); err != nil {
			t.Fatal(err)
		}
	}

	if !hasZoneEvent(events, vision.EventZoneEnter, "door") {
		t.Fatalf("eventos = %v", events)
	}
	if startsAtEnter != 0 {
		t.Fatalf("PTZStart antes de zone_enter = %d", startsAtEnter)
	}
	starts, _, _ := ptz.snapshot()
	if len(starts) == 0 {
		t.Fatal("el seguimiento no empezó después de zone_enter")
	}
}

func TestTrackingStartsWhenLeavingFrame(t *testing.T) {
	cfg := baseConfig()
	cfg.HomePreset = 1
	cfg.Zones = []vision.Zone{{
		ID: "door", Pose: vision.Pose{Preset: 1}, Points: []vision.Point{
			{X: .3, Y: .3}, {X: .4, Y: .3}, {X: .4, Y: .8}, {X: .3, Y: .8},
		},
	}}
	e, ptz, _ := newTestEngine(&cfg)

	for i := 0; i < 2; i++ {
		if err := e.processFrame(context.Background(), frame(track(4, .8, .45, .4, .55, .7))); err != nil {
			t.Fatal(err)
		}
	}
	if starts, _, _ := ptz.snapshot(); len(starts) != 0 {
		t.Fatalf("PTZ antes del tercer frame = %v", starts)
	}
	if err := e.processFrame(context.Background(), frame(track(4, .8, .85, .4, .95, .7))); err != nil {
		t.Fatal(err)
	}

	starts, _, _ := ptz.snapshot()
	if len(starts) != 1 || starts[0] != nvr.TurnRight {
		t.Fatalf("starts = %v; quería TurnRight", starts)
	}
}

func TestNoGuardedZonesTracksAfterThreeFrames(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	tracks := frame(
		track(2, .6, .15, .4, .25, .7),
		track(9, .95, .75, .4, .85, .7),
	)

	for i := 0; i < 2; i++ {
		if err := e.processFrame(context.Background(), tracks); err != nil {
			t.Fatal(err)
		}
		if starts, _, _ := ptz.snapshot(); len(starts) != 0 {
			t.Fatalf("frame %d inició PTZ: %v", i+1, starts)
		}
	}
	if err := e.processFrame(context.Background(), tracks); err != nil {
		t.Fatal(err)
	}

	starts, _, _ := ptz.snapshot()
	if len(starts) != 1 || starts[0] != nvr.TurnRight {
		t.Fatalf("starts = %v; quería el track de mayor confianza", starts)
	}
	e.mu.Lock()
	targetID := e.targetID
	e.mu.Unlock()
	if targetID != 9 {
		t.Fatalf("target = %d; quería 9", targetID)
	}
}

func TestZoomSoloDesdeZonaZoomYRespetaObjetivoYBorde(t *testing.T) {
	tests := []struct {
		name string
		zone bool
		box  vision.Box
		want bool
	}{
		{"zona zoom", true, vision.Box{.4, .45, .6, .65}, true},
		{"zona sin zoom", false, vision.Box{.4, .45, .6, .65}, false},
		{"altura objetivo", true, vision.Box{.4, .375, .6, .875}, false},
		{"toca borde", true, vision.Box{.01, .45, .21, .65}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Zones = []vision.Zone{fullZone("z", cfg.HomePreset, tt.zone)}
			e, ptz, _ := newTestEngine(&cfg)
			for i := 0; i < 3; i++ {
				_ = e.processFrame(context.Background(), frame(vision.Track{ID: 1, Conf: .9, Box: tt.box}))
			}
			starts, _, _ := ptz.snapshot()
			got := len(starts) == 1 && starts[0] == nvr.ZoomIn
			if got != tt.want {
				t.Fatalf("starts = %v, zoom = %v; quería %v", starts, got, tt.want)
			}
		})
	}
}

func TestObjetivoSeMantieneAunqueAparezcaMayorConfianza(t *testing.T) {
	cfg := baseConfig()
	e, _, _ := newTestEngine(&cfg)
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .5, .45, .45, .55, .65)))
	}
	_ = e.processFrame(context.Background(), frame(
		track(1, .5, .45, .45, .55, .65),
		track(2, .99, .1, .45, .2, .65),
	))
	e.mu.Lock()
	got := e.targetID
	e.mu.Unlock()
	if got != 1 {
		t.Fatalf("target = %d; quería 1", got)
	}
}

func TestPerdidaEmiteStopYVuelveAlPresetCeroBased(t *testing.T) {
	cfg := baseConfig()
	e, ptz, clock := newTestEngine(&cfg)
	var events []vision.Event
	e.Emit = func(event vision.Event) { events = append(events, event) }
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(7, .9, .45, .45, .55, .65)))
	}
	_ = e.processFrame(context.Background(), frame())
	clock.advance(cfg.Tracking.ReturnAfter)
	_ = e.processFrame(context.Background(), frame())
	_, _, presets := ptz.snapshot()
	if len(presets) != 1 || presets[0] != cfg.HomePreset-1 {
		t.Fatalf("presets = %v", presets)
	}
	if !hasEvent(events, vision.EventTrackingStop) {
		t.Fatalf("eventos = %v", events)
	}
}

func TestDobleFalloDesplazaYDesactivaZonaDePreset(t *testing.T) {
	cfg := baseConfig()
	cfg.Zones = []vision.Zone{fullZone("preset", cfg.HomePreset, false), fullZone("any", 0, false)}
	e, ptz, _ := newTestEngine(&cfg)
	e.Verifier = &fakeVerifier{checks: []Check{{HasReference: true}, {HasReference: true}}}
	var events []vision.Event
	e.Emit = func(event vision.Event) { events = append(events, event) }
	if err := e.Rehome(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, presets := ptz.snapshot()
	if len(presets) != 2 {
		t.Fatalf("gotos = %v", presets)
	}
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(3, .9, .45, .45, .55, .65)))
	}
	if !hasEvent(events, vision.EventCameraDisplaced) || !hasZoneEvent(events, vision.EventZoneEnter, "any") {
		t.Fatalf("eventos = %v", events)
	}
	if message := eventMessage(events, vision.EventCameraDisplaced); !strings.Contains(message, "verificación fallida") {
		t.Fatalf("mensaje camera_displaced = %q", message)
	}
	if hasZoneEvent(events, vision.EventZoneEnter, "preset") {
		t.Fatalf("zona de preset activa estando desplazada: %v", events)
	}
}

func TestGotoErrorDoesNotStopEngine(t *testing.T) {
	cfg := baseConfig()
	cfg.Zones = []vision.Zone{fullZone("always", 0, false)}
	ptz := &fakePTZ{presetErr: errors.New("NVR no disponible")}
	clock := &testClock{now: time.Unix(100, 0)}
	e := &Engine{
		Cam: 1,
		Cfg: func() vision.CameraConfig { return cfg },
		PTZ: ptz,
		Clock: Clock{
			Now: clock.Now,
			Sleep: func(context.Context, time.Duration) error {
				return nil
			},
		},
		SettleDelay: time.Millisecond,
	}
	var events []vision.Event
	e.Emit = func(event vision.Event) { events = append(events, event) }
	frames := make(chan vision.Frame)
	statuses := make(chan vision.Status)
	close(statuses)
	runDone := make(chan error, 1)
	go func() { runDone <- e.Run(context.Background(), frames, statuses) }()
	for i := 0; i < 3; i++ {
		frames <- frame(track(5, .9, .45, .4, .55, .7))
		deadline := time.Now().Add(time.Second)
		for {
			e.mu.Lock()
			seen := e.seen[5]
			e.mu.Unlock()
			if seen >= i+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("el frame %d no fue procesado", i+1)
			}
			time.Sleep(time.Millisecond)
		}
	}
	close(frames)

	if err := <-runDone; err != nil {
		t.Fatalf("Run terminó por error de Goto: %v", err)
	}
	_, _, presets := ptz.snapshot()
	if len(presets) != 2 {
		t.Fatalf("intentos de Goto = %v; quería dos", presets)
	}
	if !hasEvent(events, vision.EventCameraDisplaced) {
		t.Fatalf("falta camera_displaced: %v", events)
	}
	if message := eventMessage(events, vision.EventCameraDisplaced); !strings.Contains(message, "error del NVR") {
		t.Fatalf("mensaje camera_displaced = %q", message)
	}
	if !hasZoneEvent(events, vision.EventZoneEnter, "always") {
		t.Fatalf("falta zone_enter posterior: %v", events)
	}
}

func TestManualAbortaPulsoPausaYLuegoVuelve(t *testing.T) {
	cfg := baseConfig()
	e, ptz, clock := newTestEngine(&cfg)
	ptz.started = make(chan struct{}, 1)
	e.Clock.Sleep = func(ctx context.Context, d time.Duration) error {
		if d == pulseDuration {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	done := make(chan struct{})
	for i := 0; i < 2; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
	}
	go func() {
		defer close(done)
		_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
	}()
	<-ptz.started
	e.Manual()
	<-done
	starts, stops, _ := ptz.snapshot()
	if len(starts) != 1 || stops != 1 {
		t.Fatalf("starts = %v, stops = %d", starts, stops)
	}
	_ = e.processFrame(context.Background(), frame(track(1, .9, .8, .45, .9, .65)))
	starts, _, _ = ptz.snapshot()
	if len(starts) != 1 {
		t.Fatalf("movió en pausa: %v", starts)
	}
	clock.advance(cfg.Tracking.ReturnAfter)
	if err := e.handleWake(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, presets := ptz.snapshot()
	if len(presets) != 1 || presets[0] != cfg.HomePreset-1 {
		t.Fatalf("presets = %v", presets)
	}
}

func TestReconnectandoAOKDisparaRehome(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	_ = e.processStatus(context.Background(), vision.Status{Cam: 1, State: "reconnecting"})
	_ = e.processStatus(context.Background(), vision.Status{Cam: 1, State: "ok"})
	_, _, presets := ptz.snapshot()
	if len(presets) != 1 {
		t.Fatalf("presets = %v", presets)
	}
}

func TestCamaraSinSeguimientoNuncaLlamaPTZYEmiteZona(t *testing.T) {
	cfg := baseConfig()
	cfg.Tracking.Enabled = false
	cfg.Zones = []vision.Zone{fullZone("preset-ajeno", 99, false)}
	e, ptz, _ := newTestEngine(&cfg)
	var events []vision.Event
	e.Emit = func(event vision.Event) { events = append(events, event) }
	if err := e.Rehome(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .9, .45, .45, .55, .65)))
	}
	starts, stops, presets := ptz.snapshot()
	if len(starts) != 0 || stops != 0 || len(presets) != 0 {
		t.Fatalf("starts=%v stops=%d presets=%v", starts, stops, presets)
	}
	if !hasZoneEvent(events, vision.EventZoneEnter, "preset-ajeno") {
		t.Fatalf("eventos = %v", events)
	}
}

func TestDesactivarSeguimientoSeAplicaEnSiguienteFrame(t *testing.T) {
	cfg := baseConfig()
	e, ptz, _ := newTestEngine(&cfg)
	for i := 0; i < 3; i++ {
		_ = e.processFrame(context.Background(), frame(track(1, .9, .1, .45, .2, .65)))
	}
	cfg.Tracking.Enabled = false
	_ = e.processFrame(context.Background(), frame(track(1, .9, .8, .45, .9, .65)))
	starts, _, presets := ptz.snapshot()
	if len(starts) != 1 || len(presets) != 0 {
		t.Fatalf("starts=%v presets=%v", starts, presets)
	}
}

func hasEvent(events []vision.Event, kind vision.EventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func hasZoneEvent(events []vision.Event, kind vision.EventKind, zone string) bool {
	for _, event := range events {
		if event.Kind == kind && event.ZoneID == zone {
			return true
		}
	}
	return false
}

func eventMessage(events []vision.Event, kind vision.EventKind) string {
	for _, event := range events {
		if event.Kind == kind {
			return event.Message
		}
	}
	return ""
}
