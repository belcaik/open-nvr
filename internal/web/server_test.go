package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/talk"
)

type fakeTalkSession struct {
	frames chan []byte
	closed chan struct{}
	once   sync.Once
	send   func(context.Context, []byte) error
}

func newFakeTalkSession() *fakeTalkSession {
	return &fakeTalkSession{frames: make(chan []byte, 8), closed: make(chan struct{})}
}

func (s *fakeTalkSession) SendPCM(ctx context.Context, pcm []byte) error {
	if s.send != nil {
		return s.send(ctx, pcm)
	}
	s.frames <- append([]byte(nil), pcm...)
	return nil
}

func (s *fakeTalkSession) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

type fakeTalk struct {
	mu      sync.Mutex
	channel int
	session *fakeTalkSession
}

func (f *fakeTalk) Open(_ context.Context, channel int) (talk.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channel = channel
	if f.session == nil {
		f.session = newFakeTalkSession()
	}
	return f.session, nil
}

type fakeCameras struct {
	healthErr error
	camsErr   error
	only1     bool
	opErr     error
	active    map[int]bool
	calls     []string
}

func (f *fakeCameras) Health(context.Context) error { return f.healthErr }

func (f *fakeCameras) Cameras(context.Context) ([]nvr.Camera, error) {
	if f.camsErr != nil {
		return nil, f.camsErr
	}
	if f.only1 {
		return []nvr.Camera{{Channel: 1, Online: true}}, nil
	}
	return []nvr.Camera{{Channel: 1, Online: true, LoginStatus: "weak password"},
		{Channel: 2, Online: true}, {Channel: 3, Online: true}, {Channel: 4, Online: false, LoginStatus: "offline"}}, nil
}

func (f *fakeCameras) Snapshot(_ context.Context, ch int) ([]byte, error) {
	f.calls = append(f.calls, fmt.Sprintf("snapshot %d", ch))
	return []byte("\xff\xd8jpeg"), f.opErr
}

func (f *fakeCameras) PTZStart(_ context.Context, ch int, cmd nvr.PTZCommand, speed int) error {
	f.calls = append(f.calls, fmt.Sprintf("start %d %s %d", ch, cmd, speed))
	if f.opErr != nil {
		return f.opErr
	}
	if f.active == nil {
		f.active = map[int]bool{}
	}
	f.active[ch] = true
	return nil
}

func (f *fakeCameras) PTZKeepalive(ch int) bool { return f.active[ch] }

func (f *fakeCameras) PTZStop(_ context.Context, ch int) error {
	f.calls = append(f.calls, fmt.Sprintf("stop %d", ch))
	delete(f.active, ch)
	return nil
}

func (f *fakeCameras) Preset(_ context.Context, ch int, a nvr.PresetAction, idx int) error {
	f.calls = append(f.calls, fmt.Sprintf("preset %d %s %d", ch, a, idx))
	return f.opErr
}

func (f *fakeCameras) Light(_ context.Context, ch int, on bool) error {
	f.calls = append(f.calls, fmt.Sprintf("light %d %v", ch, on))
	return f.opErr
}

func (f *fakeCameras) Wiper(_ context.Context, ch int, on bool) error {
	f.calls = append(f.calls, fmt.Sprintf("wiper %d %v", ch, on))
	return f.opErr
}

type env struct {
	h     http.Handler
	sess  *auth.Sessions
	clock *time.Time
	cams  *fakeCameras
	talk  *fakeTalk
	play  PlaybackService
}

var testHash string

func init() {
	h, err := auth.HashPassword("clave-correcta")
	if err != nil {
		panic(err)
	}
	testHash = h
}

func newEnv(t *testing.T) *env {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	clock := &now
	nowFn := func() time.Time { return *clock }
	sess := &auth.Sessions{Key: []byte(strings.Repeat("k", 32)), Idle: 7 * 24 * time.Hour,
		MaxAge: 30 * 24 * time.Hour, Refresh: time.Hour, Now: nowFn}
	cams := &fakeCameras{}
	talker := &fakeTalk{}
	h := NewServer(Deps{
		Cameras: cams, Sessions: sess, Limiter: auth.NewLimiter(5, 15*time.Minute, nowFn),
		PassHash: testHash, User: "admin", Channels: []int{1, 2, 3, 4},
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Media: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("proxy " + r.URL.Query().Get("src")))
		}),
		Talk: talker,
	})
	return &env{h: h, sess: sess, clock: clock, cams: cams, talk: talker}
}

func (e *env) do(req *http.Request) *httptest.ResponseRecorder {
	if req.RemoteAddr == "192.0.2.1:1234" { // valor por defecto de httptest
		req.RemoteAddr = "192.168.0.50:40000"
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) session(t *testing.T) (auth.Session, *http.Cookie) {
	se, err := e.sess.New("admin")
	if err != nil {
		t.Fatal(err)
	}
	return se, e.sess.Cookie(se)
}

func form(method, target string, v url.Values) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestUnauthenticatedRedirects(t *testing.T) {
	e := newEnv(t)
	rec := e.do(httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("HX-Request", "true")
	rec = e.do(req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestLoginSuccessSetsCookie(t *testing.T) {
	e := newEnv(t)
	rec := e.do(form("POST", "/login", url.Values{"username": {"admin"}, "password": {"clave-correcta"}}))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("%d", rec.Code)
	}
	cks := rec.Result().Cookies()
	if len(cks) != 1 || cks[0].Name != auth.CookieName || !cks[0].Secure {
		t.Fatalf("%+v", cks)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cks[0])
	if rec := e.do(req); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Cámara 4") {
		t.Fatalf("%d", rec.Code)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	e := newEnv(t)
	rec := e.do(form("POST", "/login", url.Values{"username": {"admin"}, "password": {"mala"}}))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Usuario o contraseña incorrectos") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no debe dar cookie")
	}
}

func TestLoginRateLimited(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.do(form("POST", "/login", url.Values{"username": {"admin"}, "password": {"mala"}}))
	}
	rec := e.do(form("POST", "/login", url.Values{"username": {"admin"}, "password": {"clave-correcta"}}))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("%d", rec.Code)
	}
}

func TestCSRFRequired(t *testing.T) {
	e := newEnv(t)
	se, ck := e.session(t)
	req := httptest.NewRequest("POST", "/logout", nil)
	req.AddCookie(ck)
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("sin token: %d", rec.Code)
	}
	req = form("POST", "/logout", url.Values{"csrf_token": {e.sess.CSRFToken(se)}})
	req.AddCookie(ck)
	rec := e.do(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("con token: %d", rec.Code)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout debe borrar la cookie: %+v", c)
	}
}

func TestPublicIPForbidden(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("IP pública: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "192.168.0.9, 8.8.8.8")
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("XFF pública desde proxy: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "192.168.0.50:1234"
	req.Header.Set("X-Forwarded-For", "8.8.8.8")
	if rec := e.do(req); rec.Code != http.StatusOK {
		t.Fatalf("XFF de origen no confiable debe ignorarse: %d", rec.Code)
	}
}

func TestCloudflareTunnelForbidden(t *testing.T) {
	e := newEnv(t)
	for _, h := range []string{"Cf-Ray", "Cf-Connecting-Ip"} {
		req := httptest.NewRequest("GET", "/login", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set(h, "x")
		if rec := e.do(req); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d", h, rec.Code)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	rec := e.do(httptest.NewRequest("GET", "/login", nil))
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(rec.Header())
	}
}

func TestSessionRefresh(t *testing.T) {
	e := newEnv(t)
	_, ck := e.session(t)
	*e.clock = e.clock.Add(2 * time.Hour)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(ck)
	rec := e.do(req)
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("%d %v", rec.Code, rec.Result().Cookies())
	}
}

func TestStaticServed(t *testing.T) {
	e := newEnv(t)
	rec := e.do(httptest.NewRequest("GET", "/static/vendor/htmx.min.js", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "max-age=3600" {
		t.Fatalf("%d", rec.Code)
	}
}

func TestHealthPartial(t *testing.T) {
	e := newEnv(t)
	_, ck := e.session(t)
	e.cams.healthErr = errors.New("caído")
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.AddCookie(ck)
	rec := e.do(req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "El NVR no responde") ||
		strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	if rec := e.do(req); rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("%d", rec.Code)
	}
}
