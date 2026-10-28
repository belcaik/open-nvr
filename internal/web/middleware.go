package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/belcaik/open-nvr/internal/auth"
)

func itoa(n int) string { return strconv.Itoa(n) }

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"media-src 'self' blob:; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", csp)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

// clientIP usa X-Forwarded-For (último valor, el que añadió Caddy) solo si
// la conexión viene de un proxy de confianza.
func (s *server) clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	remote = remote.Unmap()
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" || !s.trusted(remote) {
		return remote
	}
	parts := strings.Split(xff, ",")
	fwd, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1]))
	if err != nil {
		return netip.Addr{}
	}
	return fwd.Unmap()
}

func (s *server) trusted(a netip.Addr) bool {
	for _, p := range s.d.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// privateOnly rechaza clientes con IP pública (defensa además de Caddy) y
// cualquier petición que haya pasado por un túnel de Cloudflare: en
// Baphomet hay túneles activos, y su tráfico llega desde localhost, así que
// la IP de origen no basta para distinguirlo.
func (s *server) privateOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cf-Ray") != "" || r.Header.Get("Cf-Connecting-Ip") != "" {
			http.Error(w, "Acceso solo desde la red local", http.StatusForbidden)
			return
		}
		ip := s.clientIP(r)
		if !ip.IsValid() || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			http.Error(w, "Acceso solo desde la red local", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type ctxKey struct{}

func sessionFrom(r *http.Request) auth.Session {
	se, _ := r.Context().Value(ctxKey{}).(auth.Session)
	return se
}

func (s *server) requireSession(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(auth.CookieName)
		var se auth.Session
		if err == nil {
			se, err = s.d.Sessions.Decode(ck.Value)
		}
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/login")
				http.Error(w, "Sesión caducada", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if s.d.Sessions.NeedsRefresh(se) {
			se = s.d.Sessions.Touch(se)
			http.SetCookie(w, s.d.Sessions.Cookie(se))
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, se)))
	})
}

func (s *server) requireCSRF(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			tok := r.Header.Get("X-CSRF-Token")
			if tok == "" {
				tok = r.PostFormValue("csrf_token")
			}
			if !s.d.Sessions.CheckCSRF(sessionFrom(r), tok) {
				http.Error(w, "Token CSRF no válido", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// sameOrigin exige que el WebSocket lo abra una página de este mismo host.
func (s *server) sameOrigin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || u.Host == "" || u.Host != r.Host {
			http.Error(w, "Origen no permitido", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}
