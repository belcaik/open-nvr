package web

import "net/http"

func (s *server) live(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "live", "En directo", struct{ Channels []int }{s.d.Channels})
}

// registerCameraRoutes añade las rutas de cada cámara (vista, captura y PTZ).
func (s *server) registerCameraRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
}
