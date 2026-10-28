package media

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/belcaik/open-nvr/internal/logx"
)

// Proxy reenvía /api/stream/ws?src=<stream> a la API WebSocket de go2rtc.
// Solo acepta los nombres de stream generados por Render.
type Proxy struct {
	allowed map[string]bool
	rp      *httputil.ReverseProxy
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
	return &Proxy{allowed: allowed, rp: rp}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if len(q) != 1 || len(q["src"]) != 1 || !p.allowed[q.Get("src")] {
		http.NotFound(w, r)
		return
	}
	p.rp.ServeHTTP(w, r)
}
