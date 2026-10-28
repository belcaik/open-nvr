package web

import (
	"context"
	"net/http"
	"time"
)

type healthStatus struct {
	NVR, Video string // "" si va bien; si no, el motivo
}

// health devuelve el parcial de la barra de estado.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	var st healthStatus
	if err := s.d.Cameras.Health(ctx); err != nil {
		st.NVR = "El NVR no responde"
	}
	if s.d.Go2RTCHealth != nil {
		if err := s.d.Go2RTCHealth(ctx); err != nil {
			st.Video = "El servicio de vídeo no responde"
		}
	}
	s.render(w, r, http.StatusOK, "_health", "", st)
}
