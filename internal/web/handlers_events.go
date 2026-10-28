package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type eventJSON struct {
	Channel int    `json:"channel"`
	Kind    string `json:"kind"`
	Active  bool   `json:"active"`
	Time    string `json:"time"`
}

// events entrega los eventos del NVR al navegador por Server-Sent Events.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}
	ch, cancel := s.d.Events.Subscribe()
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 5000\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(eventJSON{Channel: ev.Channel, Kind: ev.Kind, Active: ev.Active,
				Time: ev.Time.In(s.d.Location).Format("15:04:05")})
			fmt.Fprintf(w, "event: nvr\ndata: %s\n\n", b)
			fl.Flush()
		}
	}
}
