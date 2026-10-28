// Package web sirve la interfaz: páginas HTML con htmx, la API de control y
// el proxy de vídeo. Los handlers validan y delegan; no hay lógica de negocio.
package web

import (
	"context"
	"html/template"
	"io/fs"
	"net/http"
	"net/netip"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
)

// CameraService es lo que la web necesita del dominio de cámaras.
type CameraService interface {
	Health(ctx context.Context) error
}

type Deps struct {
	Cameras        CameraService
	Sessions       *auth.Sessions
	Limiter        *auth.Limiter
	PassHash       string
	User           string
	Media          http.Handler // proxy de señalización hacia go2rtc
	Go2RTCHealth   func(ctx context.Context) error
	TrustedProxies []netip.Prefix
	Channels       []int
	// FailDelay retrasa la respuesta a un login fallido (500 ms en producción).
	FailDelay time.Duration
}

type server struct {
	d     Deps
	pages map[string]*template.Template
}

func NewServer(d Deps) http.Handler {
	s := &server{d: d, pages: parsePages()}
	mux := http.NewServeMux()

	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", cacheFor(time.Hour, http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)

	authed := func(h http.HandlerFunc) http.Handler { return s.requireSession(s.requireCSRF(h)) }
	mux.Handle("POST /logout", authed(s.logout))
	mux.Handle("GET /{$}", authed(s.live))
	mux.Handle("GET /api/health", authed(s.health))
	if d.Media != nil {
		mux.Handle("GET /api/stream/ws", authed(s.sameOrigin(d.Media.ServeHTTP)))
	}
	s.registerCameraRoutes(mux, authed)

	return securityHeaders(s.privateOnly(mux))
}

func cacheFor(d time.Duration, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age="+itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}
