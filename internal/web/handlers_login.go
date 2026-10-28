package web

import (
	"net/http"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
)

func (s *server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "login", "Entrar", nil)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r).String()
	if s.d.Limiter.Blocked(ip) {
		s.render(w, r, http.StatusTooManyRequests, "login", "Entrar",
			"Demasiados intentos. Espera unos minutos.")
		return
	}
	user, pass := r.PostFormValue("username"), r.PostFormValue("password")
	if user != s.d.User || !auth.VerifyPassword(s.d.PassHash, pass) {
		s.d.Limiter.Fail(ip)
		time.Sleep(s.d.FailDelay)
		s.render(w, r, http.StatusUnauthorized, "login", "Entrar", "Usuario o contraseña incorrectos")
		return
	}
	s.d.Limiter.Reset(ip)
	se, err := s.d.Sessions.New(user)
	if err != nil {
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.d.Sessions.Cookie(se))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.d.Sessions.ClearCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
