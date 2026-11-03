package app

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/vision"
	"github.com/belcaik/open-nvr/internal/vision/events"
	"github.com/coder/websocket"
)

type fakePTZ struct{}

func (fakePTZ) PTZStart(context.Context, int, nvr.PTZCommand, int) error { return nil }
func (fakePTZ) PTZStop(context.Context, int) error                       { return nil }
func (fakePTZ) Preset(context.Context, int, nvr.PresetAction, int) error { return nil }

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
