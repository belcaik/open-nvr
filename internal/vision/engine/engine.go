// Package engine convierte las detecciones de una cámara en eventos de zona
// y movimientos PTZ acotados.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/vision"
)

const (
	pulseDuration      = 250 * time.Millisecond
	stopTimeout        = 3 * time.Second
	defaultSettleDelay = 4 * time.Second
	deadZoneMin        = 0.425
	deadZoneMax        = 0.575
	borderMargin       = 0.02
)

// State es el estado observable de la máquina de seguimiento.
type State string

const (
	AtPreset  State = "at_preset"
	Tracking  State = "tracking"
	Returning State = "returning"
	Paused    State = "paused"
)

// Pose es la posición que el motor puede afirmar sobre la cámara.
type Pose struct {
	Preset    int  `json:"preset"`
	Verified  bool `json:"verified"`
	Displaced bool `json:"displaced"`
}

// Snapshot devuelve el estado observable y el objetivo actual del motor.
func (e *Engine) Snapshot() (State, Pose, int) {
	e.init()
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state, e.pose, e.targetID
}

// PTZ contiene únicamente las operaciones de cámara que usa el motor.
type PTZ interface {
	PTZStart(ctx context.Context, ch int, cmd nvr.PTZCommand, speed int) error
	PTZStop(ctx context.Context, ch int) error
	Preset(ctx context.Context, ch int, action nvr.PresetAction, idx int) error
}

// Check es el resultado de comparar la imagen actual con la referencia.
type Check struct {
	HasReference bool
	Verified     bool
}

// Verifier comprueba visualmente que una cámara está en un preset.
type Verifier interface {
	Verify(ctx context.Context, cam, preset int) (Check, error)
}

// Clock hace deterministas las esperas del motor en las pruebas.
type Clock struct {
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

// Engine procesa una sola cámara. SettleDelay es opcional; cero usa 4 s.
type Engine struct {
	Cam         int
	Cfg         func() vision.CameraConfig
	PTZ         PTZ
	Verifier    Verifier
	Clock       Clock
	Emit        func(vision.Event)
	OnState     func(State, Pose)
	SettleDelay time.Duration

	once sync.Once
	mu   sync.Mutex

	state State
	pose  Pose

	occupancy vision.Occupancy
	seen      map[int]int
	targetID  int
	targetBox vision.Box
	zoom      bool
	missingAt time.Time
	ignore    int
	pan       int
	tilt      int
	zoomCount int

	activeCancel context.CancelFunc
	manualAt     time.Time
	manualSeq    uint64
	wake         chan struct{}
	eventSeq     uint64
	lastStatus   string
}

func (e *Engine) init() {
	e.once.Do(func() {
		if e.Clock.Now == nil {
			e.Clock.Now = time.Now
		}
		if e.Clock.Sleep == nil {
			e.Clock.Sleep = sleep
		}
		e.state = AtPreset
		e.wake = make(chan struct{}, 1)
		e.seen = make(map[int]int)
	})
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Run consume frames y estados hasta que se cierran ambos canales o se
// cancela ctx. Si hay acumulación de frames, descarta todos salvo el último.
func (e *Engine) Run(ctx context.Context, frames <-chan vision.Frame, statuses <-chan vision.Status) error {
	e.init()
	if e.config().Tracking.Enabled {
		if err := e.Rehome(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logx.Printf("vision engine cámara %d: rehome inicial: %v", e.Cam, err)
		}
	}

	for frames != nil || statuses != nil {
		select {
		case <-ctx.Done():
			e.cancelActive()
			return ctx.Err()
		case <-e.wake:
			if err := e.handleWake(ctx); err != nil && ctx.Err() == nil {
				logx.Printf("vision engine cámara %d: rehome programado: %v", e.Cam, err)
			}
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			latest, found := e.latestFrame(frame, frames)
			if found {
				if err := e.processFrame(ctx, latest); err != nil && ctx.Err() == nil {
					logx.Printf("vision engine cámara %d: procesar frame: %v", e.Cam, err)
				}
			}
		case status, ok := <-statuses:
			if !ok {
				statuses = nil
				continue
			}
			if status.Cam == e.Cam {
				if err := e.processStatus(ctx, status); err != nil && ctx.Err() == nil {
					logx.Printf("vision engine cámara %d: procesar estado: %v", e.Cam, err)
				}
			}
		}
	}
	return nil
}

func (e *Engine) latestFrame(first vision.Frame, frames <-chan vision.Frame) (vision.Frame, bool) {
	latest := first
	found := first.Cam == e.Cam
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return latest, found
			}
			if frame.Cam == e.Cam {
				latest, found = frame, true
			}
		default:
			return latest, found
		}
	}
}

func (e *Engine) processFrame(ctx context.Context, frame vision.Frame) error {
	cfg := e.config()
	now := e.Clock.Now()

	e.mu.Lock()
	e.updateSeen(frame.Tracks)
	state := e.state
	if !cfg.Tracking.Enabled && state != AtPreset {
		e.targetID = 0
		e.zoom = false
		e.missingAt = time.Time{}
		e.state = AtPreset
		e.pose = Pose{}
		state = AtPreset
		stateChanged := true
		e.mu.Unlock()
		e.emitEvent(vision.EventTrackingStop, "", 0, nil, now, "")
		if stateChanged {
			e.notifyState(AtPreset, Pose{})
		}
	} else {
		e.mu.Unlock()
	}

	zones := e.activeZones(cfg)
	zoneEvents := e.occupancy.Update(frame, zones, now)
	for _, event := range zoneEvents {
		kind := vision.EventZoneEnter
		if event.Type == vision.ZoneLeave {
			kind = vision.EventZoneLeave
		}
		box := event.Box
		e.emitEvent(kind, event.ZoneID, event.TrackID, &box, event.At, "")
	}

	if !cfg.Tracking.Enabled {
		return nil
	}

	e.mu.Lock()
	state = e.state
	if state == Paused || state == Returning {
		e.mu.Unlock()
		return nil
	}
	if state == AtPreset {
		target, zoom, ok := selectZoneEnterTarget(frame.Tracks, zones, zoneEvents)
		if !ok {
			target, ok = selectReadyTarget(frame.Tracks, e.seen, hasGuardedZone(zones))
		}
		if !ok {
			e.mu.Unlock()
			return nil
		}
		e.state = Tracking
		e.targetID = target.ID
		e.targetBox = target.Box
		e.zoom = zoom
		e.missingAt = time.Time{}
		e.mu.Unlock()
		box := target.Box
		e.emitEvent(vision.EventTrackingStart, "", target.ID, &box, now, "")
		e.notifyState(Tracking, e.currentPose())
		return e.trackPulse(ctx, cfg, target.Box)
	}

	var target *vision.Track
	for i := range frame.Tracks {
		if frame.Tracks[i].ID == e.targetID {
			target = &frame.Tracks[i]
			break
		}
	}
	if target == nil {
		if e.missingAt.IsZero() {
			e.missingAt = now
		}
		returnAfter := cfg.Tracking.ReturnAfter
		lost := now.Sub(e.missingAt) >= returnAfter
		trackID := e.targetID
		e.mu.Unlock()
		if lost {
			e.emitEvent(vision.EventTrackingStop, "", trackID, nil, now, "")
			return e.Rehome(ctx)
		}
		return nil
	}
	e.targetBox = target.Box
	e.missingAt = time.Time{}
	if e.ignore > 0 {
		e.ignore--
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()
	return e.trackPulse(ctx, cfg, target.Box)
}

func (e *Engine) trackPulse(ctx context.Context, cfg vision.CameraConfig, box vision.Box) error {
	e.mu.Lock()
	cmd, panDelta, tiltDelta, zoomDelta, ok := e.command(cfg.Tracking, box)
	e.mu.Unlock()
	if !ok {
		return nil
	}

	pulseCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if e.state != Tracking {
		e.mu.Unlock()
		cancel()
		return nil
	}
	e.activeCancel = cancel
	e.mu.Unlock()

	var pulseErr error
	if e.PTZ == nil {
		pulseErr = errors.New("engine: PTZ no configurado")
	} else {
		pulseErr = e.PTZ.PTZStart(pulseCtx, e.Cam, cmd, cfg.Tracking.Speed)
	}
	started := pulseErr == nil
	if started {
		pulseErr = e.Clock.Sleep(pulseCtx, pulseDuration)
	}

	// Stop no hereda la cancelación del pulso: una orden manual o la
	// cancelación de Run no deben dejar el motor moviéndose.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), stopTimeout)
	if e.PTZ != nil {
		if err := e.PTZ.PTZStop(stopCtx, e.Cam); pulseErr == nil && err != nil {
			pulseErr = err
		}
	}
	stopCancel()
	cancel()

	e.mu.Lock()
	e.activeCancel = nil
	if started {
		e.pan += panDelta
		e.tilt += tiltDelta
		e.zoomCount += zoomDelta
		e.ignore = 3
		e.pose.Displaced = true
		e.pose.Verified = false
	}
	state := e.state
	pose := e.pose
	stateChanged := false
	if pulseErr != nil && !errors.Is(pulseErr, context.Canceled) && state == Tracking {
		e.state = Paused
		state = Paused
		stateChanged = true
	}
	e.mu.Unlock()
	if started || stateChanged {
		e.notifyState(state, pose)
	}
	// Los fallos PTZ degradan el motor a Paused; no tumban el consumidor de
	// detecciones. La excepción observable es la cancelación del Run, que su
	// bucle principal atiende por ctx.Done.
	return nil
}

func (e *Engine) command(cfg vision.TrackingConfig, box vision.Box) (nvr.PTZCommand, int, int, int, bool) {
	x := (box[0] + box[2]) / 2
	y := box[1] + 0.25*(box[3]-box[1])
	panDelta, tiltDelta := 0, 0
	if x < deadZoneMin && e.pan > -cfg.MaxPanPulses {
		panDelta = -1
	} else if x > deadZoneMax && e.pan < cfg.MaxPanPulses {
		panDelta = 1
	}
	if y < deadZoneMin && e.tilt < cfg.MaxTiltPulses {
		tiltDelta = 1
	} else if y > deadZoneMax && e.tilt > -cfg.MaxTiltPulses {
		tiltDelta = -1
	}
	if panDelta != 0 || tiltDelta != 0 {
		return directionCommand(panDelta, tiltDelta), panDelta, tiltDelta, 0, true
	}
	height := box[3] - box[1]
	awayFromBorder := box[0] > borderMargin && box[1] > borderMargin && box[2] < 1-borderMargin && box[3] < 1-borderMargin
	if e.zoom && height < cfg.ZoomTargetHeight && awayFromBorder && e.zoomCount < cfg.MaxZoomPulses {
		return nvr.ZoomIn, 0, 0, 1, true
	}
	return "", 0, 0, 0, false
}

func directionCommand(pan, tilt int) nvr.PTZCommand {
	switch {
	case pan < 0 && tilt > 0:
		return nvr.TurnUpLeft
	case pan > 0 && tilt > 0:
		return nvr.TurnUpRight
	case pan < 0 && tilt < 0:
		return nvr.TurnDownLeft
	case pan > 0 && tilt < 0:
		return nvr.TurnDownRight
	case pan < 0:
		return nvr.TurnLeft
	case pan > 0:
		return nvr.TurnRight
	case tilt > 0:
		return nvr.TurnUp
	default:
		return nvr.TurnDown
	}
}

func selectReadyTarget(tracks []vision.Track, seen map[int]int, guarded bool) (vision.Track, bool) {
	var best vision.Track
	found := false
	for _, track := range tracks {
		if seen[track.ID] < 3 || (guarded && !atFrameBorder(track.Box)) {
			continue
		}
		if !found || track.Conf > best.Conf {
			best = track
			found = true
		}
	}
	return best, found
}

func atFrameBorder(box vision.Box) bool {
	x := (box[0] + box[2]) / 2
	y := (box[1] + box[3]) / 2
	return x < .15 || x > .85 || y < .15 || y > .85
}

func selectZoneEnterTarget(tracks []vision.Track, zones []vision.Zone, events []vision.ZoneEvent) (vision.Track, bool, bool) {
	for _, event := range events {
		if event.Type != vision.ZoneEnter {
			continue
		}
		for _, track := range tracks {
			if track.ID != event.TrackID {
				continue
			}
			zoom := false
			for _, zone := range zones {
				if zone.ID == event.ZoneID {
					zoom = zone.Zoom
					break
				}
			}
			return track, zoom, true
		}
	}
	return vision.Track{}, false, false
}

func hasGuardedZone(zones []vision.Zone) bool {
	for _, zone := range zones {
		if zone.Pose.Preset != 0 {
			return true
		}
	}
	return false
}

func (e *Engine) updateSeen(tracks []vision.Track) {
	present := make(map[int]struct{}, len(tracks))
	for _, track := range tracks {
		if _, exists := present[track.ID]; exists {
			continue
		}
		present[track.ID] = struct{}{}
		e.seen[track.ID]++
	}
	for id := range e.seen {
		if _, ok := present[id]; !ok {
			delete(e.seen, id)
		}
	}
}

func (e *Engine) activeZones(cfg vision.CameraConfig) []vision.Zone {
	if !cfg.Tracking.Enabled {
		return cfg.Zones
	}
	e.mu.Lock()
	pose := e.pose
	e.mu.Unlock()
	zones := make([]vision.Zone, 0, len(cfg.Zones))
	for _, zone := range cfg.Zones {
		if zone.Pose.Preset == 0 || (!pose.Displaced && zone.Pose.Preset == pose.Preset) {
			zones = append(zones, zone)
		}
	}
	return zones
}

// Manual da prioridad inmediata al usuario y cancela cualquier pulso activo.
func (e *Engine) Manual() {
	e.init()
	now := e.Clock.Now()
	e.mu.Lock()
	e.manualAt = now
	e.manualSeq++
	seq := e.manualSeq
	e.state = Paused
	e.pose.Displaced = true
	e.pose.Verified = false
	cancel := e.activeCancel
	pose := e.pose
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.notifyState(Paused, pose)

	d := e.config().Tracking.ReturnAfter
	go func() {
		if e.Clock.Sleep(context.Background(), d) != nil {
			return
		}
		e.mu.Lock()
		current := e.manualSeq == seq && e.state == Paused
		e.mu.Unlock()
		if current {
			select {
			case e.wake <- struct{}{}:
			default:
			}
		}
	}()
}

func (e *Engine) handleWake(ctx context.Context) error {
	e.mu.Lock()
	paused := e.state == Paused
	due := e.Clock.Now().Sub(e.manualAt) >= e.config().Tracking.ReturnAfter
	e.mu.Unlock()
	if paused && due && e.config().Tracking.Enabled {
		return e.rehome(ctx, true)
	}
	return nil
}

// Rehome vuelve al preset de inicio, espera la estabilización y verifica la
// imagen. Una cámara sin seguimiento nunca recibe órdenes PTZ.
func (e *Engine) Rehome(ctx context.Context) error {
	return e.rehome(ctx, false)
}

func (e *Engine) rehome(ctx context.Context, resumePaused bool) error {
	e.init()
	cfg := e.config()
	if !cfg.Tracking.Enabled {
		return nil
	}
	e.mu.Lock()
	if e.state == Paused && !resumePaused {
		e.mu.Unlock()
		return nil
	}
	returnGeneration := e.manualSeq
	e.state = Returning
	pose := e.pose
	e.mu.Unlock()
	e.notifyState(Returning, pose)

	failureMessage := "verificación fallida al volver al preset"
	for attempt := 0; attempt < 2; attempt++ {
		if !e.returnCurrent(returnGeneration) {
			return nil
		}
		var gotoErr error
		if e.PTZ == nil {
			gotoErr = errors.New("PTZ no configurado")
		} else {
			gotoErr = e.PTZ.Preset(ctx, e.Cam, nvr.PresetGoto, cfg.HomePreset-1)
		}
		if gotoErr != nil {
			logx.Printf("vision engine cámara %d: Goto al preset %d (intento %d/2): %v", e.Cam, cfg.HomePreset, attempt+1, gotoErr)
			failureMessage = fmt.Sprintf("error del NVR al volver al preset: %v", gotoErr)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if !e.returnCurrent(returnGeneration) {
			return nil
		}
		delay := e.SettleDelay
		if delay <= 0 {
			delay = defaultSettleDelay
		}
		if err := e.Clock.Sleep(ctx, delay); err != nil {
			return err
		}
		if !e.returnCurrent(returnGeneration) {
			return nil
		}
		check := Check{}
		var err error
		if e.Verifier != nil {
			check, err = e.Verifier.Verify(ctx, e.Cam, cfg.HomePreset)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failureMessage = fmt.Sprintf("verificación fallida al volver al preset: %v", err)
			continue
		}
		if err == nil && (!check.HasReference || check.Verified) {
			pose := Pose{Preset: cfg.HomePreset, Verified: check.HasReference && check.Verified}
			if e.returnCurrent(returnGeneration) {
				e.finishReturn(pose)
			}
			return nil
		}
		failureMessage = "verificación fallida al volver al preset"
	}

	displacedPose := Pose{Preset: cfg.HomePreset, Displaced: true}
	if e.returnCurrent(returnGeneration) {
		e.finishReturn(displacedPose)
		e.emitEvent(vision.EventCameraDisplaced, "", 0, nil, e.Clock.Now(), failureMessage)
	}
	return nil
}

func (e *Engine) returnCurrent(generation uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.manualSeq == generation && e.state == Returning
}

func (e *Engine) finishReturn(pose Pose) {
	e.mu.Lock()
	e.pose = pose
	e.state = AtPreset
	e.targetID = 0
	e.zoom = false
	e.missingAt = time.Time{}
	e.ignore = 0
	e.pan, e.tilt, e.zoomCount = 0, 0, 0
	e.mu.Unlock()
	e.notifyState(AtPreset, pose)
}

func (e *Engine) processStatus(ctx context.Context, status vision.Status) error {
	e.mu.Lock()
	previous := e.lastStatus
	e.lastStatus = status.State
	e.mu.Unlock()
	if previous == "reconnecting" && status.State == "ok" {
		return e.Rehome(ctx)
	}
	return nil
}

func (e *Engine) config() vision.CameraConfig {
	if e.Cfg == nil {
		return vision.CameraConfig{}
	}
	return e.Cfg()
}

func (e *Engine) cancelActive() {
	e.mu.Lock()
	cancel := e.activeCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) currentPose() Pose {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pose
}

func (e *Engine) notifyState(state State, pose Pose) {
	if e.OnState != nil {
		e.OnState(state, pose)
	}
}

func (e *Engine) emitEvent(kind vision.EventKind, zone string, track int, box *vision.Box, at time.Time, message string) {
	if e.Emit == nil {
		return
	}
	e.mu.Lock()
	e.eventSeq++
	id := fmt.Sprintf("%d-%d-%d", e.Cam, at.UnixNano(), e.eventSeq)
	e.mu.Unlock()
	e.Emit(vision.Event{ID: id, Kind: kind, Cam: e.Cam, ZoneID: zone, TrackID: track, At: at, Box: box, Message: message})
}
