package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

type tile struct {
	Channel     int
	Online      bool
	LoginStatus string
	Known       bool // el NVR informó del canal
}

type livePage struct {
	Tiles []tile
	Error string
}

func (s *server) tiles(ctx context.Context) ([]tile, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cams, err := s.d.Cameras.Cameras(ctx)
	byCh := map[int]nvr.Camera{}
	for _, c := range cams {
		byCh[c.Channel] = c
	}
	var out []tile
	for _, ch := range s.d.Channels {
		c, ok := byCh[ch]
		out = append(out, tile{Channel: ch, Online: c.Online, LoginStatus: c.LoginStatus, Known: ok})
	}
	if err != nil {
		// El vídeo puede funcionar aunque la API del NVR no responda.
		return out, "No se pudo leer la lista de cámaras del NVR"
	}
	return out, ""
}

func (s *server) live(w http.ResponseWriter, r *http.Request) {
	tiles, msg := s.tiles(r.Context())
	s.render(w, r, http.StatusOK, "live", "En directo", livePage{Tiles: tiles, Error: msg})
}

// channel valida {ch} contra los canales configurados.
func (s *server) channel(r *http.Request) (int, bool) {
	ch, err := strconv.Atoi(r.PathValue("ch"))
	return ch, err == nil && slices.Contains(s.d.Channels, ch)
}

type cameraPage struct {
	Tile  tile
	Error string
}

func (s *server) camera(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tiles, msg := s.tiles(r.Context())
	var t tile
	for _, x := range tiles {
		if x.Channel == ch {
			t = x
		}
	}
	s.render(w, r, http.StatusOK, "camera", "Cámara "+strconv.Itoa(ch), cameraPage{Tile: t, Error: msg})
}

func (s *server) snapshot(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	img, err := s.d.Cameras.Snapshot(r.Context(), ch)
	if err != nil {
		code, msg := nvrError(err)
		http.Error(w, msg, code)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition",
		`inline; filename="camara`+strconv.Itoa(ch)+`-`+time.Now().Format("20060102-150405")+`.jpg"`)
	w.Write(img)
}

// registerCameraRoutes añade las rutas de cada cámara (vista, captura y PTZ).
func (s *server) registerCameraRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /camera/{ch}", authed(s.camera))
	mux.Handle("GET /camera/{ch}/snapshot", authed(s.snapshot))
	mux.Handle("POST /api/ptz/{ch}/start", authed(s.ptzStart))
	mux.Handle("POST /api/ptz/{ch}/keepalive", authed(s.ptzKeepalive))
	mux.Handle("POST /api/ptz/{ch}/stop", authed(s.ptzStop))
	mux.Handle("POST /api/ptz/{ch}/preset/{action}/{idx}", authed(s.ptzPreset))
	mux.Handle("POST /api/ptz/{ch}/{device}/{state}", authed(s.ptzSwitch))
}
