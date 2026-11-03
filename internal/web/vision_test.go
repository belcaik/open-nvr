package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
	"github.com/belcaik/open-nvr/internal/vision"
	visionapp "github.com/belcaik/open-nvr/internal/vision/app"
	"github.com/belcaik/open-nvr/internal/vision/events"
	"github.com/belcaik/open-nvr/internal/vision/pose"
)

type fakeVision struct {
	store   *vision.Store
	history *events.History
	manual  []int
	updates chan visionapp.Update
}

func newFakeVision(t *testing.T) *fakeVision {
	t.Helper()
	dir := t.TempDir()
	store, err := vision.NewStore(filepath.Join(dir, "vision.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeVision{store: store, history: &events.History{Dir: filepath.Join(dir, "events")}, updates: make(chan visionapp.Update, 8)}
}

func (f *fakeVision) Config(ch int) vision.CameraConfig {
	if cfg, ok := f.store.Get(ch); ok {
		return cfg
	}
	return visionapp.DefaultConfig()
}
func (f *fakeVision) SetConfig(ch int, cfg vision.CameraConfig) error { return f.store.Put(ch, cfg) }
func (f *fakeVision) References(int) map[int]visionapp.ReferenceState {
	return map[int]visionapp.ReferenceState{}
}
func (f *fakeVision) CaptureReference(context.Context, int, int) (pose.Mode, error) {
	return pose.Day, nil
}
func (f *fakeVision) Manual(ch int) { f.manual = append(f.manual, ch) }
func (f *fakeVision) Subscribe(int) (<-chan visionapp.Update, func()) {
	f.updates <- visionapp.Update{Type: "state", State: visionapp.State{State: "at_preset", Detector: "ok"}}
	return f.updates, func() {}
}
func (f *fakeVision) History() *events.History { return f.history }

func TestVisionConfigRoundTripReturnAfterSeconds(t *testing.T) {
	e, _ := visionTestEnv(t)
	get := e.do(e.authed(t, http.MethodGet, "/api/vision/1/config", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"return_after_s":20`) {
		t.Fatalf("GET: %d %s", get.Code, get.Body.String())
	}
	body := `{"enabled":true,"home_preset":1,"tracking":{"enabled":true,"speed":5,"zoom_target_height":0.5,"return_after_s":25,"max_pan_pulses":20,"max_tilt_pulses":10,"max_zoom_pulses":10},"zones":[],"references":{"1":{"day":true}}}`
	put := visionJSONRequest(t, e, http.MethodPut, "/api/vision/1/config", body, true)
	if rec := e.do(put); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"return_after_s":25`) {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
}

func TestVisionInvalidConfigDoesNotChangeStoredValue(t *testing.T) {
	e, v := visionTestEnv(t)
	before := v.Config(1)
	body := `{"enabled":true,"home_preset":1,"tracking":{"enabled":true,"speed":99,"zoom_target_height":0.5,"return_after_s":20,"max_pan_pulses":20,"max_tilt_pulses":10,"max_zoom_pulses":10},"zones":[]}`
	rec := e.do(visionJSONRequest(t, e, http.MethodPut, "/api/vision/1/config", body, true))
	if rec.Code != http.StatusBadRequest || v.Config(1).Tracking.Speed != before.Tracking.Speed {
		t.Fatalf("respuesta=%d config=%+v", rec.Code, v.Config(1))
	}
}

func TestVisionConfigPutRequiresCSRF(t *testing.T) {
	e, _ := visionTestEnv(t)
	rec := e.do(visionJSONRequest(t, e, http.MethodPut, "/api/vision/1/config", `{}`, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestVisionSSEStartsWithState(t *testing.T) {
	e, _ := visionTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req := e.authed(t, http.MethodGet, "/api/vision/1/stream", nil).WithContext(ctx)
	req.RemoteAddr = "192.168.0.50:40000"
	w := newSignalWriter()
	done := make(chan struct{})
	go func() { e.h.ServeHTTP(w, req); close(done) }()
	select {
	case <-w.flushed:
	case <-time.After(time.Second):
		t.Fatal("el SSE no escribió")
	}
	for deadline := time.Now().Add(time.Second); !strings.Contains(w.String(), "event: state"); {
		if time.Now().After(deadline) {
			t.Fatalf("SSE = %q", w.String())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestVisionEventsFiltersCamAndKinds(t *testing.T) {
	e, v := visionTestEnv(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for _, event := range []vision.Event{{ID: "keep", Cam: 1, Kind: vision.EventZoneEnter, At: base},
		{ID: "kind", Cam: 1, Kind: vision.EventZoneLeave, At: base.Add(time.Minute)},
		{ID: "cam", Cam: 2, Kind: vision.EventZoneEnter, At: base.Add(2 * time.Minute)}} {
		if _, err := v.history.Record(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.do(e.authed(t, http.MethodGet, "/api/vision/events?cam=1&kind=zone_enter", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"keep"`) || strings.Contains(rec.Body.String(), `"id":"kind"`) || strings.Contains(rec.Body.String(), `"id":"cam"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestVisionSnapshotTraversalReturnsNotFound(t *testing.T) {
	e, _ := visionTestEnv(t)
	rec := e.do(e.authed(t, http.MethodGet, "/vision/snapshots/%2e%2e/secret", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestVisionManualRunsBeforePTZCommand(t *testing.T) {
	e, v := visionTestEnv(t)
	rec := e.do(e.authed(t, http.MethodPost, "/api/ptz/1/start", url.Values{"cmd": {"TurnLeft"}, "speed": {"5"}}))
	if rec.Code != http.StatusNoContent || len(v.manual) != 1 || v.manual[0] != 1 {
		t.Fatalf("status=%d manual=%v", rec.Code, v.manual)
	}
}

func TestVisionRoutesAbsentWithoutService(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/vision/1/config", "/api/vision/events", "/vision/events"} {
		if rec := e.do(e.authed(t, http.MethodGet, path, nil)); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
}

func visionTestEnv(t *testing.T) (*env, *fakeVision) {
	e := newEnv(t)
	v := newFakeVision(t)
	e.h = NewServer(Deps{Cameras: e.cams, Sessions: e.sess, Limiter: eLimiter(t), PassHash: testHash,
		User: "admin", Channels: []int{1, 2, 3, 4}, Vision: v})
	return e, v
}

func eLimiter(t *testing.T) *auth.Limiter {
	t.Helper()
	return auth.NewLimiter(5, 15*time.Minute, time.Now)
}

func visionJSONRequest(t *testing.T, e *env, method, target, body string, csrf bool) *http.Request {
	t.Helper()
	se, cookie := e.session(t)
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-CSRF-Token", e.sess.CSRFToken(se))
	}
	req.AddCookie(cookie)
	return req
}

type signalWriter struct {
	mu      sync.Mutex
	header  http.Header
	body    bytes.Buffer
	flushed chan struct{}
	once    sync.Once
}

func newSignalWriter() *signalWriter {
	return &signalWriter{header: make(http.Header), flushed: make(chan struct{})}
}
func (w *signalWriter) Header() http.Header { return w.header }
func (w *signalWriter) WriteHeader(int)     {}
func (w *signalWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(data)
}
func (w *signalWriter) Flush() { w.once.Do(func() { close(w.flushed) }) }
func (w *signalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func TestVisionPagesRenderWithVision(t *testing.T) {
	e, _ := visionTestEnv(t)
	for path, want := range map[string]string{
		"/camera/1":      `id="vision-overlay"`,
		"/vision/events": `href="/vision/events"`,
	} {
		rec := e.do(e.authed(t, http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: %d, sin %q", path, rec.Code, want)
		}
	}
}
