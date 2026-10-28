package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/belcaik/open-nvr/internal/nvr"
)

// authed prepara una petición con sesión y, si hace falta, token CSRF.
func (e *env) authed(t *testing.T, method, target string, v url.Values) *http.Request {
	se, ck := e.session(t)
	var req *http.Request
	if method == "GET" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = form(method, target, v)
		req.Header.Set("X-CSRF-Token", e.sess.CSRFToken(se))
	}
	req.AddCookie(ck)
	return req
}

func TestLiveListsCameras(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.authed(t, "GET", "/", nil))
	body := rec.Body.String()
	for i := 1; i <= 4; i++ {
		if !strings.Contains(body, fmt.Sprintf(`data-cam="%d"`, i)) {
			t.Fatalf("falta la cámara %d", i)
		}
	}
	if !strings.Contains(body, "/static/js/video.js") || strings.Contains(body, "notice") {
		t.Fatal(body)
	}
}

func TestLiveNVRDown(t *testing.T) {
	e := newEnv(t)
	e.cams.camsErr = nvr.ErrUnavailable
	rec := e.do(e.authed(t, "GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "No se pudo leer la lista de cámaras") ||
		strings.Count(rec.Body.String(), `class="tile"`) != 4 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCameraPage(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.authed(t, "GET", "/camera/1", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `data-quality="main"`) ||
		!strings.Contains(body, `data-ptz="TurnUpLeft"`) || !strings.Contains(body, "weak password") {
		t.Fatalf("%d %s", rec.Code, body)
	}
}

func TestCameraInvalidChannel(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/camera/9", "/camera/x", "/camera/0/snapshot"} {
		if rec := e.do(e.authed(t, "GET", p, nil)); rec.Code != http.StatusNotFound {
			t.Fatalf("%s → %d", p, rec.Code)
		}
	}
}

func TestSnapshot(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.authed(t, "GET", "/camera/2/snapshot", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestStreamProxyRequiresAuth(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/api/stream/ws?src=cam1", nil)
	req.Header.Set("Origin", "http://example.com")
	if rec := e.do(req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}

func TestStreamProxyChecksOrigin(t *testing.T) {
	e := newEnv(t)
	req := e.authed(t, "GET", "https://nvr.belcaik.com/api/stream/ws?src=cam1", nil)
	req.Header.Set("Origin", "https://evil.example")
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("otro origen: %d", rec.Code)
	}
	req = e.authed(t, "GET", "https://nvr.belcaik.com/api/stream/ws?src=cam1", nil)
	req.Header.Set("Origin", "https://nvr.belcaik.com")
	if rec := e.do(req); rec.Code != 200 || rec.Body.String() != "proxy cam1" {
		t.Fatalf("mismo origen: %d %s", rec.Code, rec.Body)
	}
}

func TestPTZStartKeepaliveStop(t *testing.T) {
	e := newEnv(t)
	steps := []struct {
		path string
		v    url.Values
		code int
	}{
		{"/api/ptz/1/start", url.Values{"cmd": {"TurnLeft"}, "speed": {"5"}}, 204},
		{"/api/ptz/1/keepalive", nil, 204},
		{"/api/ptz/1/stop", nil, 204},
		{"/api/ptz/1/keepalive", nil, http.StatusConflict},
	}
	for _, s := range steps {
		if rec := e.do(e.authed(t, "POST", s.path, s.v)); rec.Code != s.code {
			t.Fatalf("%s → %d (esperado %d)", s.path, rec.Code, s.code)
		}
	}
	if want := []string{"start 1 TurnLeft 5", "stop 1"}; !reflect.DeepEqual(e.cams.calls, want) {
		t.Fatal(e.cams.calls)
	}
}

func TestPTZInvalidCommand(t *testing.T) {
	e := newEnv(t)
	for _, v := range []url.Values{
		{"cmd": {"AutoLeftRight"}, "speed": {"5"}},
		{"cmd": {"TurnLeft"}, "speed": {"11"}},
		{"cmd": {"TurnLeft"}},
	} {
		if rec := e.do(e.authed(t, "POST", "/api/ptz/1/start", v)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v → %d", v, rec.Code)
		}
	}
	if len(e.cams.calls) != 0 {
		t.Fatal(e.cams.calls)
	}
}

func TestPTZRequiresCSRF(t *testing.T) {
	e := newEnv(t)
	req := e.authed(t, "POST", "/api/ptz/1/stop", nil)
	req.Header.Del("X-CSRF-Token")
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPresetIndexTranslation(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.authed(t, "POST", "/api/ptz/1/preset/goto/3", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Yendo al preset 3") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if e.cams.calls[0] != "preset 1 Goto 2" {
		t.Fatal(e.cams.calls)
	}
	for _, p := range []string{"/api/ptz/1/preset/goto/0", "/api/ptz/1/preset/goto/257", "/api/ptz/1/preset/jump/1"} {
		if rec := e.do(e.authed(t, "POST", p, nil)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d", p, rec.Code)
		}
	}
}

func TestLightOff(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.authed(t, "POST", "/api/ptz/2/light/off", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Luz apagada") || e.cams.calls[0] != "light 2 false" {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, e.cams.calls)
	}
}

func TestPTZNVRDown(t *testing.T) {
	e := newEnv(t)
	e.cams.opErr = fmt.Errorf("x: %w", nvr.ErrUnavailable)
	rec := e.do(e.authed(t, "POST", "/api/ptz/1/start", url.Values{"cmd": {"TurnUp"}, "speed": {"3"}}))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "El NVR no responde") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do(e.authed(t, "POST", "/api/ptz/1/wiper/on", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `class="error"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
