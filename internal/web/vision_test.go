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
	"github.com/belcaik/open-nvr/internal/vision/snapsign"
)

type fakeVision struct {
	store   *vision.Store
	history *events.History
	signer  snapsign.Signer
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
	return &fakeVision{
		store:   store,
		history: &events.History{Dir: filepath.Join(dir, "events")},
		signer:  snapsign.Signer{Key: bytes.Repeat([]byte{0x42}, 32)},
		updates: make(chan visionapp.Update, 8),
	}
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
	f.updates <- visionapp.Update{Type: "state", State: visionapp.State{State: "at_preset", Detector: "ok", Camera: "online"}}
	return f.updates, func() {}
}
func (f *fakeVision) History() *events.History { return f.history }
func (f *fakeVision) VerifySnapshot(rel, sig string) bool {
	return f.signer.Verify(rel, sig)
}

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
	if !strings.Contains(w.String(), `"camera":"online"`) {
		t.Fatalf("SSE sin estado de cámara: %q", w.String())
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

func TestVisionEventsAcceptsCameraConnectionKinds(t *testing.T) {
	e, v := visionTestEnv(t)
	base := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	for i, kind := range []vision.EventKind{vision.EventCameraOffline, vision.EventCameraOnline} {
		if _, err := v.history.Record(context.Background(), vision.Event{ID: string(kind), Cam: 3, Kind: kind, At: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	rec := e.do(e.authed(t, http.MethodGet, "/api/vision/events?kind=camera_offline&kind=camera_online", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"kind":"camera_offline"`) || !strings.Contains(rec.Body.String(), `"kind":"camera_online"`) {
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

type fakeSnap struct{}

func (fakeSnap) Snapshot(context.Context, int) ([]byte, error) { return []byte("\xff\xd8jpeg"), nil }

// La página construye la URL quitando el prefijo "snapshots/" que guarda el historial.
func TestVisionSnapshotServedFromRecordedPath(t *testing.T) {
	e, v := visionTestEnv(t)
	v.history.Snap = fakeSnap{}
	ev, err := v.history.Record(context.Background(), vision.Event{ID: "1-1-1", Cam: 1, Kind: vision.EventZoneEnter, At: time.Now()})
	if err != nil || ev.Snapshot == "" {
		t.Fatalf("Record: %v, snapshot %q", err, ev.Snapshot)
	}
	rec := e.do(e.authed(t, http.MethodGet, "/vision/snapshots/"+strings.TrimPrefix(ev.Snapshot, "snapshots/"), nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("snapshot %q: %d", ev.Snapshot, rec.Code)
	}
}

func TestVisionSnapshotSignedAndUnauthenticatedCases(t *testing.T) {
	e, v := visionTestEnv(t)
	v.history.Snap = fakeSnap{}
	base := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	ev1, err := v.history.Record(context.Background(), vision.Event{ID: "1-1-a", Cam: 1, Kind: vision.EventZoneEnter, At: base})
	if err != nil || ev1.Snapshot == "" {
		t.Fatalf("Record ev1: %v, snapshot %q", err, ev1.Snapshot)
	}
	ev2, err := v.history.Record(context.Background(), vision.Event{ID: "1-1-b", Cam: 1, Kind: vision.EventZoneEnter, At: base})
	if err != nil || ev2.Snapshot == "" {
		t.Fatalf("Record ev2: %v, snapshot %q", err, ev2.Snapshot)
	}
	urlPath1 := "/vision/snapshots/" + strings.TrimPrefix(ev1.Snapshot, "snapshots/")
	sig1 := v.signer.Sign(ev1.Snapshot)
	sig2 := v.signer.Sign(ev2.Snapshot)

	// Firma válida sin sesión → 200 image/jpeg y Cache-Control.
	rec := e.do(httptest.NewRequest(http.MethodGet, urlPath1+"?sig="+sig1, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("firma válida sin sesión: status=%d ct=%q cc=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}

	// Firma ausente o inválida sin sesión → mismo comportamiento que hoy (303 a /login, o 401 con HX-Request).
	for _, target := range []string{urlPath1, urlPath1 + "?sig=invalida", urlPath1 + "?sig=" + sig2} {
		rec := e.do(httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Fatalf("%s sin sesión: status=%d location=%q", target, rec.Code, rec.Header().Get("Location"))
		}
		hxReq := httptest.NewRequest(http.MethodGet, target, nil)
		hxReq.Header.Set("HX-Request", "true")
		hxRec := e.do(hxReq)
		if hxRec.Code != http.StatusUnauthorized || hxRec.Header().Get("HX-Redirect") != "/login" {
			t.Fatalf("%s HX sin sesión: status=%d hx-redirect=%q", target, hxRec.Code, hxRec.Header().Get("HX-Redirect"))
		}
	}

	// Traversal con firma → 404.
	travSig := v.signer.Sign("snapshots/../secret")
	for _, target := range []string{
		"/vision/snapshots/%2e%2e/secret?sig=" + travSig,
		"/vision/snapshots/2026-11-04%5c1-1-a.jpg?sig=" + v.signer.Sign(`snapshots/2026-11-04\1-1-a.jpg`),
	} {
		rec := e.do(httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("traversal %s: status=%d, quiero 404", target, rec.Code)
		}
	}

	// Con sesión sigue funcionando sin firma y responde Cache-Control.
	authedRec := e.do(e.authed(t, http.MethodGet, urlPath1, nil))
	if authedRec.Code != http.StatusOK || authedRec.Header().Get("Content-Type") != "image/jpeg" || authedRec.Header().Get("Cache-Control") != "private, max-age=86400" {
		t.Fatalf("con sesión: status=%d ct=%q cc=%q", authedRec.Code, authedRec.Header().Get("Content-Type"), authedRec.Header().Get("Cache-Control"))
	}
}
