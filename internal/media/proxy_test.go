package media

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
