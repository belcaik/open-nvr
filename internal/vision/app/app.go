// Package app ensambla el detector, los motores y el historial de visión.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/vision"
	"github.com/belcaik/open-nvr/internal/vision/engine"
	"github.com/belcaik/open-nvr/internal/vision/events"
	"github.com/belcaik/open-nvr/internal/vision/pose"
)

var (
	watchdogPeriod       = time.Second
	watchdogDownAfter    = 15 * time.Second
	referenceSettleDelay = 4 * time.Second
)

// Config contiene las dependencias de la aplicación de visión.
type Config struct {
	DetectURL, ConfigPath, DataDir, Go2RTCBase string
	Channels                                   []int
	PTZ                                        engine.PTZ
	Notifier                                   *notify.Notifier
}

// ReferenceState resume las referencias visuales disponibles por preset.
type ReferenceState struct {
	Day   bool `json:"day"`
	Night bool `json:"night"`
}

// State es el estado enviado a la web.
type State struct {
	State    engine.State `json:"state"`
	Pose     engine.Pose  `json:"pose"`
	Detector string       `json:"detector"`
}

// Frame es la parte pública de una detección.
type Frame struct {
	Seq    uint64         `json:"seq"`
	Tracks []vision.Track `json:"tracks"`
	Target int            `json:"target"`
}

// Update es una actualización del stream web.
type Update struct {
	Type  string
	Frame Frame
	State State
	Event vision.Event
}

type cameraRuntime struct {
	engine    *engine.Engine
	frames    chan vision.Frame
	statuses  chan vision.Status
	lastFrame atomic.Int64
}

// App une los componentes de visión y es la fachada consumida por la web.
type App struct {
	client     *vision.Client
	store      *vision.Store
	refs       *pose.RefStore
	verifier   *pose.Verifier
	bus        *events.Bus
	history    *events.History
	dispatcher *events.Dispatcher
	ptz        engine.PTZ
	cameras    map[int]*cameraRuntime

	mu       sync.RWMutex
	states   map[int]State
	subs     map[uint64]subscriber
	nextSub  uint64
	lastSent map[int]time.Time
	running  atomic.Bool
}

type subscriber struct {
	cam int
	ch  chan Update
}

type verifierAdapter struct{ verifier *pose.Verifier }

func (v verifierAdapter) Verify(ctx context.Context, cam, preset int) (engine.Check, error) {
	check, err := v.verifier.Verify(ctx, cam, preset)
	return engine.Check{HasReference: check.HasReference, Verified: check.Verified}, err
}

// New construye la aplicación y carga la configuración persistida.
func New(cfg Config) (*App, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o750); err != nil {
		return nil, fmt.Errorf("crear directorio de configuración de visión: %w", err)
	}
	store, err := vision.NewStore(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	refs := &pose.RefStore{Dir: filepath.Join(cfg.DataDir, "refs")}
	verifier := &pose.Verifier{Grab: &pose.Go2RTC{Base: cfg.Go2RTCBase}, Refs: refs}
	bus := &events.Bus{}
	history := &events.History{Dir: filepath.Join(cfg.DataDir, "events"), Snap: events.Go2RTCSnapshotter{Base: cfg.Go2RTCBase}}
	dispatcher := &events.Dispatcher{Bus: bus, History: history}
	if cfg.Notifier != nil {
		dispatcher.Sinks = append(dispatcher.Sinks, &events.NtfySink{Notifier: cfg.Notifier})
	}
	a := &App{
		client: vision.NewClient(cfg.DetectURL), store: store, refs: refs, verifier: verifier,
		bus: bus, history: history, dispatcher: dispatcher, ptz: cfg.PTZ,
		cameras: make(map[int]*cameraRuntime), states: make(map[int]State),
		subs: make(map[uint64]subscriber), lastSent: make(map[int]time.Time),
	}
	dispatcher.OnEvent = func(event vision.Event) {
		a.publish(event.Cam, Update{Type: "vision", Event: event})
	}
	for _, ch := range cfg.Channels {
		if ch < 1 {
			return nil, fmt.Errorf("canal de visión inválido: %d", ch)
		}
		if _, exists := a.cameras[ch]; exists {
			continue
		}
		runtime := &cameraRuntime{frames: make(chan vision.Frame, 2), statuses: make(chan vision.Status, 4)}
		runtime.engine = &engine.Engine{
			Cam: ch, PTZ: cfg.PTZ, Verifier: verifierAdapter{verifier},
			Cfg:  func() vision.CameraConfig { return engineConfig(a.Config(ch)) },
			Emit: a.bus.Publish,
		}
		runtime.engine.OnState = func(state engine.State, cameraPose engine.Pose) {
			a.setEngineState(ch, state, cameraPose)
		}
		a.cameras[ch] = runtime
		a.states[ch] = State{State: engine.AtPreset, Detector: "starting"}
	}
	return a, nil
}

// engineConfig apaga zonas y seguimiento cuando la visión de la cámara está
// desactivada: el motor nunca mueve ni avisa por una cámara apagada.
func engineConfig(cfg vision.CameraConfig) vision.CameraConfig {
	if !cfg.Enabled {
		cfg.Zones = nil
		cfg.Tracking.Enabled = false
	}
	return cfg
}

// DefaultConfig devuelve los valores del contrato para una cámara nueva.
func DefaultConfig() vision.CameraConfig {
	return vision.CameraConfig{
		HomePreset: 1, Zones: []vision.Zone{},
		Tracking: vision.TrackingConfig{Speed: 5, ZoomTargetHeight: .5, ReturnAfter: 20 * time.Second,
			MaxPanPulses: 20, MaxTiltPulses: 10, MaxZoomPulses: 10},
	}
}

// Run inicia todos los consumidores y termina al cancelarse ctx.
func (a *App) Run(ctx context.Context) {
	if !a.running.CompareAndSwap(false, true) {
		<-ctx.Done()
		return
	}
	defer a.running.Store(false)
	var workers sync.WaitGroup
	dispatchReady := make(chan struct{})
	workers.Add(1)
	go func() {
		defer workers.Done()
		a.dispatcher.RunReady(ctx, dispatchReady)
	}()
	<-dispatchReady
	for _, runtime := range a.cameras {
		runtime := runtime
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := runtime.engine.Run(ctx, runtime.frames, runtime.statuses); err != nil && !errors.Is(err, context.Canceled) {
				logx.Printf("vision: motor terminado: %v", err)
			}
		}()
	}
	frames, statuses := a.client.Start(ctx)
	workers.Add(1)
	go func() {
		defer workers.Done()
		a.watch(ctx)
	}()
	defer workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			a.routeFrame(frame)
		case status, ok := <-statuses:
			if !ok {
				statuses = nil
				continue
			}
			a.routeStatus(status)
		}
	}
}

func (a *App) routeFrame(frame vision.Frame) {
	runtime := a.cameras[frame.Cam]
	if runtime == nil {
		return
	}
	runtime.lastFrame.Store(time.Now().UnixNano())
	putLatest(runtime.frames, frame)
	a.mu.Lock()
	last := a.lastSent[frame.Cam]
	if time.Since(last) < 100*time.Millisecond {
		a.mu.Unlock()
		return
	}
	a.lastSent[frame.Cam] = time.Now()
	a.mu.Unlock()
	_, _, target := runtime.engine.Snapshot()
	a.publish(frame.Cam, Update{Type: "frame", Frame: Frame{Seq: frame.Seq, Tracks: frame.Tracks, Target: target}})
}

func (a *App) routeStatus(status vision.Status) {
	runtime := a.cameras[status.Cam]
	if runtime == nil {
		return
	}
	putLatest(runtime.statuses, status)
	a.mu.Lock()
	state := a.states[status.Cam]
	state.Detector = status.State
	a.states[status.Cam] = state
	a.mu.Unlock()
	a.publish(status.Cam, Update{Type: "state", State: state})
}

func putLatest[T any](ch chan T, value T) {
	select {
	case ch <- value:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- value:
	default:
	}
}

func (a *App) setEngineState(cam int, state engine.State, cameraPose engine.Pose) {
	a.mu.Lock()
	current := a.states[cam]
	current.State, current.Pose = state, cameraPose
	a.states[cam] = current
	a.mu.Unlock()
	a.publish(cam, Update{Type: "state", State: current})
}

func (a *App) watch(ctx context.Context) {
	ticker := time.NewTicker(watchdogPeriod)
	defer ticker.Stop()
	down := make(map[int]bool)
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			connected := a.client.Connected()
			for cam, runtime := range a.cameras {
				cfg := a.Config(cam)
				last := time.Unix(0, runtime.lastFrame.Load())
				stale := cfg.Enabled && (runtime.lastFrame.Load() == 0 && now.Sub(started) >= watchdogDownAfter || runtime.lastFrame.Load() != 0 && now.Sub(last) >= watchdogDownAfter)
				failed := !connected && now.Sub(started) >= watchdogDownAfter || stale
				if failed && !down[cam] {
					down[cam] = true
					a.bus.Publish(vision.Event{ID: fmt.Sprintf("%d-%d-down", cam, now.UnixNano()), Kind: vision.EventDetectorDown, Cam: cam, At: now, Message: "detector sin conexión o sin frames"})
				}
				if !failed {
					down[cam] = false
				}
				a.mu.Lock()
				state := a.states[cam]
				previousDetector := state.Detector
				if !connected {
					state.Detector = "down"
				} else if state.Detector == "down" {
					state.Detector = "starting"
				}
				a.states[cam] = state
				a.mu.Unlock()
				if state.Detector != previousDetector {
					a.publish(cam, Update{Type: "state", State: state})
				}
			}
		}
	}
}

// Config devuelve la configuración guardada o los valores por defecto.
func (a *App) Config(ch int) vision.CameraConfig {
	if cfg, ok := a.store.Get(ch); ok {
		return cfg
	}
	return DefaultConfig()
}

// SetConfig valida y guarda una configuración.
func (a *App) SetConfig(ch int, cfg vision.CameraConfig) error {
	if a.cameras[ch] == nil {
		return fmt.Errorf("canal de visión desconocido: %d", ch)
	}
	return a.store.Put(ch, cfg)
}

// References informa de las referencias usadas por la configuración.
func (a *App) References(ch int) map[int]ReferenceState {
	cfg := a.Config(ch)
	presets := map[int]struct{}{}
	if cfg.HomePreset > 0 {
		presets[cfg.HomePreset] = struct{}{}
	}
	for _, zone := range cfg.Zones {
		if zone.Pose.Preset > 0 {
			presets[zone.Pose.Preset] = struct{}{}
		}
	}
	result := make(map[int]ReferenceState, len(presets))
	for preset := range presets {
		result[preset] = ReferenceState{Day: a.refs.Exists(ch, preset, pose.Day), Night: a.refs.Exists(ch, preset, pose.Night)}
	}
	return result
}

// CaptureReference mueve al preset y guarda la vista estabilizada.
func (a *App) CaptureReference(ctx context.Context, ch, preset int) (pose.Mode, error) {
	if preset < 1 {
		return pose.Day, fmt.Errorf("preset inválido: debe ser mayor o igual a 1")
	}
	if a.cameras[ch] == nil {
		return pose.Day, fmt.Errorf("canal de visión desconocido: %d", ch)
	}
	if a.ptz == nil {
		return pose.Day, errors.New("PTZ no configurado")
	}
	// Es una orden del usuario: el motor se aparta mientras dura.
	a.Manual(ch)
	if err := a.ptz.Preset(ctx, ch, nvr.PresetGoto, preset-1); err != nil {
		return pose.Day, fmt.Errorf("ir al preset: %w", err)
	}
	timer := time.NewTimer(referenceSettleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return pose.Day, ctx.Err()
	case <-timer.C:
	}
	return a.verifier.Capture(ctx, ch, preset)
}

// Manual aparta el motor automático ante una orden del usuario.
func (a *App) Manual(ch int) {
	if runtime := a.cameras[ch]; runtime != nil {
		runtime.engine.Manual()
	}
}

// Subscribe abre un stream acotado y entrega inmediatamente el estado.
func (a *App) Subscribe(ch int) (<-chan Update, func()) {
	out := make(chan Update, 32)
	a.mu.Lock()
	a.nextSub++
	id := a.nextSub
	a.subs[id] = subscriber{cam: ch, ch: out}
	state := a.states[ch]
	a.mu.Unlock()
	out <- Update{Type: "state", State: state}
	var once sync.Once
	return out, func() {
		once.Do(func() {
			a.mu.Lock()
			if _, ok := a.subs[id]; ok {
				delete(a.subs, id)
				close(out)
			}
			a.mu.Unlock()
		})
	}
}

func (a *App) publish(cam int, update Update) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, sub := range a.subs {
		if sub.cam != cam {
			continue
		}
		select {
		case sub.ch <- update:
		default:
		}
	}
}

// History devuelve el historial compartido con el dispatcher.
func (a *App) History() *events.History { return a.history }
