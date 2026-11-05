package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/vision"
	visionapp "github.com/belcaik/open-nvr/internal/vision/app"
	"github.com/belcaik/open-nvr/internal/vision/events"
)

type trackingConfigJSON struct {
	Enabled          bool    `json:"enabled"`
	Speed            int     `json:"speed"`
	ZoomTargetHeight float64 `json:"zoom_target_height"`
	ReturnAfterS     int64   `json:"return_after_s"`
	MaxPanPulses     int     `json:"max_pan_pulses"`
	MaxTiltPulses    int     `json:"max_tilt_pulses"`
	MaxZoomPulses    int     `json:"max_zoom_pulses"`
}

type cameraConfigJSON struct {
	Enabled    bool                             `json:"enabled"`
	HomePreset int                              `json:"home_preset"`
	Tracking   trackingConfigJSON               `json:"tracking"`
	Zones      []vision.Zone                    `json:"zones"`
	References map[int]visionapp.ReferenceState `json:"references"`
}

func (s *server) registerVisionRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/vision/{ch}/stream", authed(s.visionStream))
	mux.Handle("GET /api/vision/{ch}/config", authed(s.visionConfig))
	mux.Handle("PUT /api/vision/{ch}/config", authed(s.putVisionConfig))
	mux.Handle("POST /api/vision/{ch}/reference", authed(s.captureVisionReference))
	mux.Handle("GET /api/vision/events", authed(s.visionHistory))
	authedSnapshot := authed(s.visionSnapshot)
	mux.HandleFunc("GET /vision/snapshots/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if sig := r.URL.Query().Get("sig"); sig != "" && s.d.Vision.VerifySnapshot("snapshots/"+r.PathValue("path"), sig) {
			s.visionSnapshot(w, r)
			return
		}
		authedSnapshot.ServeHTTP(w, r)
	})
	mux.Handle("GET /vision/events", authed(s.visionEventsPage))
}

func configJSON(cfg vision.CameraConfig, references map[int]visionapp.ReferenceState) cameraConfigJSON {
	return cameraConfigJSON{Enabled: cfg.Enabled, HomePreset: cfg.HomePreset, Zones: cfg.Zones, References: references,
		Tracking: trackingConfigJSON{Enabled: cfg.Tracking.Enabled, Speed: cfg.Tracking.Speed,
			ZoomTargetHeight: cfg.Tracking.ZoomTargetHeight, ReturnAfterS: int64(cfg.Tracking.ReturnAfter / time.Second),
			MaxPanPulses: cfg.Tracking.MaxPanPulses, MaxTiltPulses: cfg.Tracking.MaxTiltPulses,
			MaxZoomPulses: cfg.Tracking.MaxZoomPulses}}
}

func (c cameraConfigJSON) internal() vision.CameraConfig {
	return vision.CameraConfig{Enabled: c.Enabled, HomePreset: c.HomePreset, Zones: c.Zones,
		Tracking: vision.TrackingConfig{Enabled: c.Tracking.Enabled, Speed: c.Tracking.Speed,
			ZoomTargetHeight: c.Tracking.ZoomTargetHeight, ReturnAfter: time.Duration(c.Tracking.ReturnAfterS) * time.Second,
			MaxPanPulses: c.Tracking.MaxPanPulses, MaxTiltPulses: c.Tracking.MaxTiltPulses,
			MaxZoomPulses: c.Tracking.MaxZoomPulses}}
}

func (s *server) visionConfig(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, configJSON(s.d.Vision.Config(ch), s.d.Vision.References(ch)))
}

func (s *server) putVisionConfig(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body cameraConfigJSON
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}
	if err := s.d.Vision.SetConfig(ch, body.internal()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, configJSON(s.d.Vision.Config(ch), s.d.Vision.References(ch)))
}

func (s *server) captureVisionReference(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Preset int `json:"preset"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.Preset < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preset inválido: debe ser mayor o igual a 1"})
		return
	}
	mode, err := s.d.Vision.CaptureReference(r.Context(), ch, body.Preset)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": mode.String()})
}

func (s *server) visionStream(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}
	updates, unsubscribe := s.d.Vision.Subscribe(ch)
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 5000\n\n")
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case update, ok := <-updates:
			if !ok {
				return
			}
			var data any
			switch update.Type {
			case "frame":
				data = update.Frame
			case "state":
				data = update.State
			case "vision":
				data = update.Event
			default:
				continue
			}
			encoded, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", update.Type, encoded)
			flusher.Flush()
		}
	}
}

func (s *server) visionHistory(w http.ResponseWriter, r *http.Request) {
	filter := events.Filter{Limit: 50}
	if raw := r.URL.Query().Get("cam"); raw != "" {
		cam, err := strconv.Atoi(raw)
		if err != nil || cam < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cam inválida"})
			return
		}
		filter.Cam = cam
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit inválido"})
			return
		}
		filter.Limit = limit
	}
	for _, kind := range r.URL.Query()["kind"] {
		filter.Kinds = append(filter.Kinds, vision.EventKind(kind))
	}
	result, err := s.d.Vision.History().List(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "leer historial"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": result})
}

func (s *server) visionSnapshot(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if rel == "" || strings.Contains(rel, "\\") {
		http.NotFound(w, r)
		return
	}
	path, err := s.d.Vision.History().SnapshotPath(filepath.Join("snapshots", rel))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *server) visionEventsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "vision_events", "Eventos de visión", nil)
}
