package media

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/belcaik/open-nvr/internal/logx"
)

// Proxy reenvía /api/stream/ws?src=<stream> a la API WebSocket de go2rtc.
// Solo acepta los nombres de stream generados por Render.
type Proxy struct {
	mu        sync.RWMutex
	allowed   map[string]bool
	temporary map[string]*temporaryStream
	rp        *httputil.ReverseProxy
}

type temporaryStream struct {
	connected    func()
	disconnected func()
}

func NewProxy(apiListen string, channels []int) *Proxy {
	allowed := map[string]bool{}
	for _, ch := range channels {
		for _, sub := range []bool{false, true} {
			for _, h264 := range []bool{false, true} {
				allowed[StreamName(ch, sub, h264)] = true
			}
		}
	}
	rp := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		src := r.In.URL.Query().Get("src")
		r.Out.URL = &url.URL{Scheme: "http", Host: apiListen, Path: "/api/ws",
			RawQuery: url.Values{"src": {src}}.Encode()}
		r.Out.Host = apiListen
		r.Out.Header.Del("Cookie")
		r.Out.Header.Del("Authorization")
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		logx.Printf("proxy de vídeo: %v", err)
		http.Error(w, "El servicio de vídeo no responde", http.StatusBadGateway)
	}}
	return &Proxy{allowed: allowed, temporary: map[string]*temporaryStream{}, rp: rp}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if len(q) != 1 || len(q["src"]) != 1 {
		http.NotFound(w, r)
		return
	}
	src := q.Get("src")
	p.mu.RLock()
	allowed := p.allowed[src]
	temporary := p.temporary[src]
	p.mu.RUnlock()
	if !allowed && temporary == nil {
		http.NotFound(w, r)
		return
	}
	if temporary != nil && temporary.connected != nil {
		temporary.connected()
	}
	p.rp.ServeHTTP(w, r)
	if temporary != nil && temporary.disconnected != nil {
		temporary.disconnected()
	}
}

// AllowTemporary autoriza exactamente un nombre opaco hasta que remove sea
// llamado. Las notificaciones permiten coordinar reconexiones del playback.
func (p *Proxy) AllowTemporary(name string, connected, disconnected func()) (remove func()) {
	if !temporaryName(name) {
		return func() {}
	}
	t := &temporaryStream{connected: connected, disconnected: disconnected}
	p.mu.Lock()
	p.temporary[name] = t
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if p.temporary[name] == t {
				delete(p.temporary, name)
			}
			p.mu.Unlock()
		})
	}
}
