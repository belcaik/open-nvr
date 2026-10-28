package nvr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// request es una petición registrada por el NVR falso.
type request struct {
	method      string
	uri         string // ruta y query, p. ej. "/Snapshot/1/RemoteImageCapture?ImageFormat=2"
	body        string
	auth        string
	contentType string
}

// fakeNVR sirve los fixtures de testdata/nvr enrutando por "MÉTODO /ruta",
// como haría el NVR real, y registra todo lo que recibe.
type fakeNVR struct {
	mu     sync.Mutex
	reqs   []request
	routes map[string]string // "GET /x" -> archivo de fixture
	raw    map[string][]byte // "GET /x" -> cuerpo literal (imágenes, etc.)
	srv    *httptest.Server
}

const fixtureDir = "../../testdata/nvr"

func newFakeNVR(t *testing.T) (*fakeNVR, *Client) {
	t.Helper()
	f := &fakeNVR{
		routes: map[string]string{
			"GET /System/DeviceInfo":                   fixtureDir + "/device_info.xml",
			"GET /System/FrontConnectDevicetList":      fixtureDir + "/front_connect_list.xml",
			"GET /System/FrontConnectDeviceStatusList": fixtureDir + "/front_connect_status.xml",
		},
		raw: map[string][]byte{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f, New(f.srv.URL, "admin", "clave")
}

func (f *fakeNVR) handle(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, request{
		method:      r.Method,
		uri:         r.URL.RequestURI(),
		body:        string(b),
		auth:        r.Header.Get("Authorization"),
		contentType: r.Header.Get("Content-Type"),
	})
	key := r.Method + " " + r.URL.Path
	file, okFile := f.routes[key]
	body, okRaw := f.raw[key]
	f.mu.Unlock()
	if okRaw {
		w.Write(body)
		return
	}
	if !okFile {
		// PUT y rutas sin fixture específico: confirmación genérica del NVR.
		file = fixtureDir + "/ok.xml"
	}
	http.ServeFile(w, r, file)
}

// setRoute apunta una ruta a otro fixture (p. ej. una respuesta de error).
func (f *fakeNVR) setRoute(key, file string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = file
}

// last devuelve la última petición registrada a una ruta.
func (f *fakeNVR) last(t *testing.T, method, path string) request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		r := f.reqs[i]
		if r.method == method && strings.HasPrefix(r.uri, path) {
			return r
		}
	}
	t.Fatalf("no hubo petición %s %s", method, path)
	return request{}
}

func (f *fakeNVR) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New(srv.URL, "admin", "mal")
	if _, err := c.DeviceInfo(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, quiero ErrUnauthorized", err)
	}
}

func TestUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // el servidor ya no responde cuando llegue la petición
	c := New(url, "admin", "clave")
	if _, err := c.DeviceInfo(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, quiero ErrUnavailable", err)
	}
}

func TestStatusCodeNonZero(t *testing.T) {
	f, c := newFakeNVR(t)
	// El NVR responde 200 con un ResponseStatus de error (caso LightCap).
	f.setRoute("PUT /PTZ/1/Light", fixtureDir+"/status_minus1.xml")
	err := c.Light(context.Background(), 1, true)
	var bre *BadResponseError
	if !errors.As(err, &bre) {
		t.Fatalf("err = %v (%T), quiero *BadResponseError", err, err)
	}
	if bre.Status != "-1" {
		t.Fatalf("Status = %q, quiero %q", bre.Status, "-1")
	}
}

func TestHTMLNotFound(t *testing.T) {
	f, c := newFakeNVR(t)
	// El NVR responde 200 con una página HTML "Not Found" (caso PTZCap).
	f.setRoute("GET /System/DeviceInfo", fixtureDir+"/not_found.html")
	_, err := c.DeviceInfo(context.Background())
	var bre *BadResponseError
	if !errors.As(err, &bre) {
		t.Fatalf("err = %v (%T), quiero *BadResponseError", err, err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	var current, maxSeen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := current.Add(1)
		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		current.Add(-1)
		b, _ := os.ReadFile(fixtureDir + "/device_info.xml")
		w.Write(b)
	}))
	defer srv.Close()
	c := New(srv.URL, "admin", "clave")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.DeviceInfo(context.Background()); err != nil {
				t.Errorf("DeviceInfo = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := maxSeen.Load(); got != 2 {
		t.Fatalf("máximo de peticiones simultáneas = %d, quiero 2", got)
	}
}

func TestSnapshotReturnsJPEG(t *testing.T) {
	f, c := newFakeNVR(t)
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF-jpeg-falso\xff\xd9")
	f.mu.Lock()
	f.raw["GET /Snapshot/1/RemoteImageCapture"] = jpeg
	f.mu.Unlock()
	b, err := c.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, jpeg) {
		t.Fatalf("snapshot = %x", b)
	}
	req := f.last(t, "GET", "/Snapshot/1/RemoteImageCapture")
	if !strings.Contains(req.uri, "ImageFormat=2") {
		t.Fatalf("uri = %q, falta ImageFormat=2", req.uri)
	}
}
