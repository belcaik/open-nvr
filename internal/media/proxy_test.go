package media

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// backend simula la API de go2rtc: acepta el upgrade y devuelve lo que reciba.
func backend(t *testing.T, gotPath *string, gotCookie *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.RequestURI()
		*gotCookie = r.Header.Get("Cookie")
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			w.WriteHeader(400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		buf := make([]byte, 5)
		io.ReadFull(rw, buf)
		conn.Write(buf)
	}))
}

func TestProxyRejectsUnknownSrc(t *testing.T) {
	var path, cookie string
	be := backend(t, &path, &cookie)
	defer be.Close()
	p := NewProxy(strings.TrimPrefix(be.URL, "http://"), []int{1, 2})
	for _, src := range []string{"cam9", "rtsp://x", "", "cam1_sub&x=1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/stream/ws?src="+src, nil)
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("src %q → %d", src, rec.Code)
		}
	}
	if path != "" {
		t.Fatal("el backend no debía recibir nada")
	}
}

func TestProxyForwardsWebSocket(t *testing.T) {
	var path, cookie string
	be := backend(t, &path, &cookie)
	defer be.Close()
	front := httptest.NewServer(NewProxy(strings.TrimPrefix(be.URL, "http://"), []int{1}))
	defer front.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET /api/stream/ws?src=cam1_sub HTTP/1.1\r\nHost: x\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nCookie: opennvr_session=secreta\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("%v %v", err, resp)
	}
	io.WriteString(conn, "hola!")
	buf := make([]byte, 5)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "hola!" {
		t.Fatalf("eco: %v %q", err, buf)
	}
	if path != "/api/ws?src=cam1_sub" {
		t.Fatalf("ruta = %q", path)
	}
	if cookie != "" {
		t.Fatal("la cookie de sesión no debe llegar a go2rtc")
	}
}

func TestProxyAllowsTemporaryNameUntilWebSocketDisconnects(t *testing.T) {
	var path, cookie string
	be := backend(t, &path, &cookie)
	defer be.Close()
	p := NewProxy(strings.TrimPrefix(be.URL, "http://"), []int{1})
	connected := make(chan struct{})
	disconnected := make(chan struct{})
	name := "playback_0123456789abcdef0123456789abcdef"
	remove := p.AllowTemporary(name, func() { close(connected) }, func() { close(disconnected) })
	defer remove()
	front := httptest.NewServer(p)
	defer front.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "GET /api/stream/ws?src="+name+" HTTP/1.1\r\nHost: x\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %+v", err, resp)
	}
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("no notificó la conexión")
	}
	conn.Close()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("no notificó la desconexión")
	}
	remove()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stream/ws?src="+name, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stream retirado = %d", rec.Code)
	}
}

func TestGo2RTCStreamsUsesDocumentedAPI(t *testing.T) {
	var requests []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		return testResponse(http.StatusNoContent), nil
	})}
	client, err := NewGo2RTCStreams("127.0.0.1:1984", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	source := "ffmpeg:http://127.0.0.1:8095/internal/playback/token#input=hevc#video=h264"
	name := "playback_0123456789abcdef0123456789abcdef"
	if err := client.Add(context.Background(), name, source); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	wantPut := "PUT /api/streams?name=" + name + "&src=" + url.QueryEscape(source)
	if len(requests) != 2 || requests[0] != wantPut || requests[1] != "DELETE /api/streams?src="+name {
		t.Fatalf("requests = %v", requests)
	}
}

func TestGo2RTCStreamsVerifiesMutationAfterReadOnlyConfigError(t *testing.T) {
	present := false
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodPut:
			present = true
			return testResponse(http.StatusBadRequest), nil
		case http.MethodDelete:
			present = false
			return testResponse(http.StatusBadRequest), nil
		case http.MethodGet:
			if !present {
				return testResponse(http.StatusNotFound), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"producers":[],"consumers":[]}`))}, nil
		}
		return testResponse(http.StatusMethodNotAllowed), nil
	})}
	client, err := NewGo2RTCStreams("127.0.0.1:1984", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	name := "playback_0123456789abcdef0123456789abcdef"
	if err := client.Add(context.Background(), name, "ffmpeg:http://source#input=hevc#video=h264"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), name); err != nil {
		t.Fatal(err)
	}
}

func TestGo2RTCStreamsRejectsNonLoopbackAndCredentialedSources(t *testing.T) {
	if _, err := NewGo2RTCStreams("192.168.0.20:1984", nil); err == nil {
		t.Fatal("aceptó una API go2rtc no local")
	}
	requests := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return testResponse(http.StatusNoContent), nil
	})}
	client, err := NewGo2RTCStreams("127.0.0.1:1984", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	name := "playback_0123456789abcdef0123456789abcdef"
	if err := client.Add(context.Background(), name, "ffmpeg:http://user:pass@127.0.0.1/source#input=hevc#video=h264"); err == nil {
		t.Fatal("aceptó credenciales en la fuente")
	}
	if requests != 0 {
		t.Fatalf("hizo %d requests con una fuente inválida", requests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
}
