package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/vision"
	"github.com/belcaik/open-nvr/internal/vision/engine"
	"github.com/belcaik/open-nvr/internal/vision/events"
	"github.com/coder/websocket"
)

type fakePTZ struct{}

func (fakePTZ) PTZStart(context.Context, int, nvr.PTZCommand, int) error { return nil }
func (fakePTZ) PTZStop(context.Context, int) error                       { return nil }
func (fakePTZ) Preset(context.Context, int, nvr.PresetAction, int) error { return nil }

type recordingPTZ struct {
	mu    sync.Mutex
	gotos []int
}

func (*recordingPTZ) PTZStart(context.Context, int, nvr.PTZCommand, int) error { return nil }
func (*recordingPTZ) PTZStop(context.Context, int) error                       { return nil }
func (p *recordingPTZ) Preset(_ context.Context, ch int, action nvr.PresetAction, _ int) error {
	if action == nvr.PresetGoto {
		p.mu.Lock()
		p.gotos = append(p.gotos, ch)
		p.mu.Unlock()
	}
	return nil
}

type connectedVerifier struct{}

func (connectedVerifier) Verify(context.Context, int, int) (engine.Check, error) {
	return engine.Check{}, nil
}

type fakeSnapshotter struct{}

func (fakeSnapshotter) Snapshot(context.Context, int) ([]byte, error) { return []byte("jpeg"), nil }

func TestVisionAppRoutesZoneEventFrameAndState(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"),
		DataDir: dir, Go2RTCBase: "http://go2rtc.test", Channels: []int{1}, PTZ: fakePTZ{}})
	if err != nil {
		t.Fatal(err)
	}
	a.history.Snap = fakeSnapshotter{}
	a.client = vision.NewClientWithHTTPClient("ws://detector.test/detections", &http.Client{Transport: wsMemoryTransport{handler: detectorFramesHandler()}})
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Zones = []vision.Zone{{ID: "puerta", Name: "Puerta", Alert: true, Points: []vision.Point{{X: .3, Y: .5}, {X: .7, Y: .5}, {X: .7, Y: 1}, {X: .3, Y: 1}}}}
	if err := a.SetConfig(1, cfg); err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := a.Subscribe(1)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	seenFrame, seenState, seenEvent := false, false, false
	deadline := time.After(3 * time.Second)
	for !(seenFrame && seenState && seenEvent) {
		select {
		case update := <-updates:
			switch update.Type {
			case "frame":
				seenFrame = true
			case "state":
				seenState = true
			case "vision":
				seenEvent = seenEvent || update.Event.Kind == vision.EventZoneEnter
			}
		case <-deadline:
			t.Fatalf("actualizaciones: frame=%t state=%t event=%t", seenFrame, seenState, seenEvent)
		}
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		stored, err := a.History().List(context.Background(), events.Filter{Cam: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) > 0 && stored[0].Kind == vision.EventZoneEnter {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("zone_enter no llegó al historial")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestVisionAppWatchdogEmitsDetectorDownOnce(t *testing.T) {
	oldPeriod, oldAfter := watchdogPeriod, watchdogDownAfter
	watchdogPeriod, watchdogDownAfter = 5*time.Millisecond, 20*time.Millisecond
	defer func() { watchdogPeriod, watchdogDownAfter = oldPeriod, oldAfter }()
	dir := t.TempDir()
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"),
		DataDir: dir, Channels: []int{1}, PTZ: fakePTZ{}})
	if err != nil {
		t.Fatal(err)
	}
	a.client = vision.NewClientWithHTTPClient("ws://detector.test/detections", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("detector caído")
	})})
	eventCh, unsubscribe := a.bus.Subscribe(8)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	count := 0
	timer := time.NewTimer(90 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event := <-eventCh:
			if event.Kind == vision.EventDetectorDown {
				count++
			}
		case <-timer.C:
			if count != 1 {
				t.Fatalf("detector_down = %d, quiero 1", count)
			}
			return
		}
	}
}

func TestVisionAppWatchdogSilencesDisabledStatus(t *testing.T) {
	oldPeriod, oldAfter := watchdogPeriod, watchdogDownAfter
	watchdogPeriod, watchdogDownAfter = 5*time.Millisecond, 15*time.Millisecond
	defer func() { watchdogPeriod, watchdogDownAfter = oldPeriod, oldAfter }()
	dir := t.TempDir()
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"),
		DataDir: dir, Channels: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	state := a.states[1]
	state.Detector = "disabled"
	a.states[1] = state
	a.mu.Unlock()
	eventCh, unsubscribe := a.bus.Subscribe(4)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.watch(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	for len(eventCh) > 0 {
		if event := <-eventCh; event.Kind == vision.EventDetectorDown {
			t.Fatal("el vigilante emitió detector_down para status disabled")
		}
	}
}

func TestOfflineCameraIsDisabledEmitsOnceAndSilencesWatchdog(t *testing.T) {
	oldWatchPeriod, oldDownAfter := watchdogPeriod, watchdogDownAfter
	oldPollPeriod := cameraPollPeriod
	watchdogPeriod, watchdogDownAfter, cameraPollPeriod = 5*time.Millisecond, 20*time.Millisecond, 10*time.Millisecond
	defer func() {
		watchdogPeriod, watchdogDownAfter, cameraPollPeriod = oldWatchPeriod, oldDownAfter, oldPollPeriod
	}()
	dir := t.TempDir()
	a, err := New(Config{
		DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"), DataDir: dir,
		Channels: []int{1, 3}, PTZ: fakePTZ{}, Cameras: func(context.Context) ([]nvr.Camera, error) {
			return []nvr.Camera{{Channel: 1, Online: true}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, cam := range []int{1, 3} {
		cfg := DefaultConfig()
		cfg.Enabled = true
		if err := a.SetConfig(cam, cfg); err != nil {
			t.Fatal(err)
		}
	}
	enabled := make(chan []int, 8)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			defer conn.Close(websocket.StatusNormalClosure, "fin")
			for {
				_, data, err := conn.Read(context.Background())
				if err != nil {
					return
				}
				var message struct {
					Enabled []int `json:"enabled"`
				}
				if json.Unmarshal(data, &message) == nil {
					enabled <- message.Enabled
				}
			}
		}()
	})
	a.client = vision.NewClientWithHTTPClient("ws://detector.test/detections", &http.Client{Transport: wsMemoryTransport{handler: handler}})
	eventCh, unsubscribe := a.bus.Subscribe(32)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-enabled:
			if fmt.Sprint(got) == "[1]" {
				goto disabled
			}
		case <-deadline:
			t.Fatal("la cámara 3 no salió de enabled")
		}
	}

disabled:
	offline, detectorDown := 0, 0
	timer := time.NewTimer(90 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case event := <-eventCh:
			if event.Cam == 3 && event.Kind == vision.EventCameraOffline {
				offline++
			}
			if event.Cam == 3 && event.Kind == vision.EventDetectorDown {
				detectorDown++
			}
		case <-timer.C:
			if offline != 1 || detectorDown != 0 {
				t.Fatalf("camera_offline=%d detector_down=%d", offline, detectorDown)
			}
			return
		}
	}
}

func TestCameraQueryErrorPreservesPreviousState(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"), DataDir: dir,
		Channels: []int{3}, Cameras: func(context.Context) ([]nvr.Camera, error) {
			calls++
			if calls == 1 {
				return []nvr.Camera{{Channel: 3, Online: false}}, nil
			}
			return nil, errors.New("NVR no disponible")
		}})
	if err != nil {
		t.Fatal(err)
	}
	a.pollCameras(context.Background())
	a.pollCameras(context.Background())
	if got := a.cameraState(3); got != "offline" {
		t.Fatalf("camera = %q, quiero offline", got)
	}
}

func TestCameraOfflineToOnlineEmitsEventAndRehomes(t *testing.T) {
	dir := t.TempDir()
	ptz := &recordingPTZ{}
	calls := 0
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"), DataDir: dir,
		Channels: []int{3}, PTZ: ptz, Cameras: func(context.Context) ([]nvr.Camera, error) {
			calls++
			return []nvr.Camera{{Channel: 3, Online: calls > 1}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Enabled, cfg.Tracking.Enabled = true, true
	if err := a.SetConfig(3, cfg); err != nil {
		t.Fatal(err)
	}
	a.cameras[3].engine.Verifier = connectedVerifier{}
	a.cameras[3].engine.Clock.Sleep = func(context.Context, time.Duration) error { return nil }
	eventCh, unsubscribe := a.bus.Subscribe(8)
	defer unsubscribe()
	a.pollCameras(context.Background())
	a.pollCameras(context.Background())
	seenOnline := false
	for len(eventCh) > 0 {
		if event := <-eventCh; event.Kind == vision.EventCameraOnline {
			seenOnline = true
		}
	}
	ptz.mu.Lock()
	gotos := append([]int(nil), ptz.gotos...)
	ptz.mu.Unlock()
	if !seenOnline || fmt.Sprint(gotos) != "[3]" {
		t.Fatalf("camera_online=%t Goto=%v", seenOnline, gotos)
	}
}

func TestCameraStateArrivesThroughSubscribe(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{DetectURL: "ws://detector.test/detections", ConfigPath: filepath.Join(dir, "vision.json"), DataDir: dir,
		Channels: []int{1}, Cameras: func(context.Context) ([]nvr.Camera, error) {
			return []nvr.Camera{{Channel: 1, Online: true}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := a.Subscribe(1)
	defer unsubscribe()
	if initial := <-updates; initial.State.Camera != "unknown" {
		t.Fatalf("camera inicial = %q", initial.State.Camera)
	}
	a.pollCameras(context.Background())
	select {
	case update := <-updates:
		if update.Type != "state" || update.State.Camera != "online" {
			t.Fatalf("update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("State.Camera no llegó por Subscribe")
	}
}

func detectorFramesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			defer conn.Close(websocket.StatusNormalClosure, "fin")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"status","cam":1,"state":"ok","fps":10}`))
			for seq := 1; seq <= 3; seq++ {
				message := fmt.Sprintf(`{"type":"detections","cam":1,"seq":%d,"ts":"2026-11-03T15:30:01Z","w":800,"h":448,"tracks":[{"id":7,"conf":0.9,"box":[0.4,0.4,0.6,0.8]}]}`, seq)
				_ = conn.Write(ctx, websocket.MessageText, []byte(message))
				time.Sleep(110 * time.Millisecond)
			}
			<-ctx.Done()
		}()
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type wsMemoryTransport struct{ handler http.Handler }

func (transport wsMemoryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clientConn, serverConn := net.Pipe()
	recorder := httptest.NewRecorder()
	hijacker := memoryHijacker{ResponseRecorder: recorder, conn: serverConn}
	transport.handler.ServeHTTP(hijacker, request)
	response := recorder.Result()
	if response.StatusCode == http.StatusSwitchingProtocols {
		response.Body = clientConn
	} else {
		clientConn.Close()
		serverConn.Close()
	}
	return response, nil
}

type memoryHijacker struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (h memoryHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

func TestDisabledCameraHidesZonesAndTracking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tracking.Enabled = true
	cfg.Zones = []vision.Zone{{ID: "z"}}
	got := engineConfig(cfg)
	if got.Tracking.Enabled || len(got.Zones) != 0 {
		t.Fatalf("cámara desactivada con seguimiento o zonas: %+v", got)
	}
	cfg.Enabled = true
	if got := engineConfig(cfg); !got.Tracking.Enabled || len(got.Zones) != 1 {
		t.Fatalf("cámara activada perdió su configuración: %+v", got)
	}
}

func TestAppSignAndVerifySnapshot(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{
		DetectURL:  "ws://detector.test/detections",
		ConfigPath: filepath.Join(dir, "vision.json"),
		DataDir:    dir,
		Channels:   []int{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	rel := "snapshots/2026-11-04/1-1-enter.jpg"
	sig := a.SignSnapshot(rel)
	if len(sig) != 64 || !a.VerifySnapshot(rel, sig) {
		t.Fatalf("firma = %q, verify = %v", sig, a.VerifySnapshot(rel, sig))
	}
	if a.VerifySnapshot("snapshots/2026-11-04/otro.jpg", sig) {
		t.Fatal("VerifySnapshot aceptó otra ruta")
	}

	// Si LoadOrCreateKey falla (p. ej. tamaño inválido), deja el firmador sin clave.
	badDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(badDir, "snapshot-url.key"), []byte("corta"), 0o600); err != nil {
		t.Fatal(err)
	}
	aBad, err := New(Config{
		DetectURL:  "ws://detector.test/detections",
		ConfigPath: filepath.Join(badDir, "vision.json"),
		DataDir:    badDir,
		Channels:   []int{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := aBad.SignSnapshot(rel); got != "" {
		t.Fatalf("SignSnapshot sin clave = %q, quiero cadena vacía", got)
	}
	if aBad.VerifySnapshot(rel, sig) {
		t.Fatal("VerifySnapshot sin clave devolvió true")
	}
}
