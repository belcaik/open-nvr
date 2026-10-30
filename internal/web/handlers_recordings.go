package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/belcaik/open-nvr/internal/playback"
)

type calDay struct {
	Day      int // 0 = hueco antes del día 1
	Has      bool
	Selected bool
	Link     string
}

type timelineBar struct {
	X, W  float64 // minutos desde las 00:00 (SVG de 1440 de ancho)
	Type  string
	Label string
	Start string
	End   string
}

type recordingsPage struct {
	Channel   int
	Channels  []int
	Day       time.Time
	MonthName string
	PrevMonth string
	NextMonth string
	Weeks     [][]calDay
	DaysErr   string
	Bars      []timelineBar
	SegErr    string
	Total     string
	Playback  bool
}

var monthNames = [...]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio",
	"agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func (s *server) recordings(w http.ResponseWriter, r *http.Request) {
	loc := s.d.Location
	ch := s.d.Channels[0]
	if v, err := strconv.Atoi(r.URL.Query().Get("ch")); err == nil && slices.Contains(s.d.Channels, v) {
		ch = v
	}
	now := time.Now().In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if v := r.URL.Query().Get("date"); v != "" {
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			http.Error(w, "Fecha no válida", http.StatusBadRequest)
			return
		}
		day = d
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	p := recordingsPage{Channel: ch, Channels: s.d.Channels, Day: day, Playback: s.d.Playback != nil,
		MonthName: fmt.Sprintf("%s de %d", monthNames[day.Month()], day.Year())}
	link := func(d time.Time) string {
		return fmt.Sprintf("/recordings?ch=%d&date=%s", ch, d.Format("2006-01-02"))
	}
	first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, loc)
	p.PrevMonth = link(first.AddDate(0, -1, 0))
	p.NextMonth = link(first.AddDate(0, 1, 0))

	days, err := s.d.Recordings.RecordDays(ctx, day.Year(), day.Month())
	if err != nil {
		_, p.DaysErr = nvrError(err)
	}
	// Calendario con semanas de lunes a domingo.
	var week []calDay
	for i := 0; i < (int(first.Weekday())+6)%7; i++ {
		week = append(week, calDay{})
	}
	for d := first; d.Month() == first.Month(); d = d.AddDate(0, 0, 1) {
		week = append(week, calDay{Day: d.Day(), Has: slices.Contains(days, d.Day()),
			Selected: d.Day() == day.Day(), Link: link(d)})
		if len(week) == 7 {
			p.Weeks = append(p.Weeks, week)
			week = nil
		}
	}
	if len(week) > 0 {
		for len(week) < 7 {
			week = append(week, calDay{})
		}
		p.Weeks = append(p.Weeks, week)
	}

	segs, err := s.d.Recordings.RecordSegments(ctx, ch, day)
	if err != nil {
		_, p.SegErr = nvrError(err)
	}
	var total time.Duration
	end := day.AddDate(0, 0, 1)
	for _, sg := range segs {
		st, en := sg.Start, sg.End
		if st.Before(day) {
			st = day
		}
		if en.After(end) {
			en = end
		}
		if !en.After(st) {
			continue
		}
		total += en.Sub(st)
		p.Bars = append(p.Bars, timelineBar{
			X:     st.Sub(day).Minutes(),
			W:     max(en.Sub(st).Minutes(), 1), // al menos 1 min para que se vea
			Type:  sg.Type,
			Label: fmt.Sprintf("%s – %s (%s)", st.Format("15:04:05"), en.Format("15:04:05"), sg.Type),
			Start: st.Format(time.RFC3339),
			End:   en.Format(time.RFC3339),
		})
	}
	p.Total = fmt.Sprintf("%dh %02dm grabadas", int(total.Hours()), int(total.Minutes())%60)
	s.render(w, r, http.StatusOK, "recordings", "Grabaciones", p)
}

func (s *server) registerPlaybackRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/playbacks", authed(s.openPlayback))
	mux.Handle("GET /api/playbacks/{id}", authed(s.playbackStatus))
	mux.Handle("POST /api/playbacks/{id}/pause", authed(s.pausePlayback))
	mux.Handle("POST /api/playbacks/{id}/resume", authed(s.resumePlayback))
	mux.Handle("POST /api/playbacks/{id}/seek", authed(s.seekPlayback))
	mux.Handle("DELETE /api/playbacks/{id}", authed(s.closePlayback))
}

func (s *server) openPlayback(w http.ResponseWriter, r *http.Request) {
	channel, err := strconv.Atoi(r.PostFormValue("channel"))
	if err != nil {
		http.Error(w, "Canal no válido", http.StatusBadRequest)
		return
	}
	start, err := time.Parse(time.RFC3339, r.PostFormValue("start"))
	if err != nil {
		http.Error(w, "Inicio no válido", http.StatusBadRequest)
		return
	}
	end, err := time.Parse(time.RFC3339, r.PostFormValue("end"))
	if err != nil || !end.After(start) {
		http.Error(w, "Fin no válido", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	view, err := s.d.Playback.Open(ctx, playback.Request{Channel: channel, Start: start, End: end})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, playback.ErrInvalidRequest) {
			status = http.StatusBadRequest
		} else if errors.Is(err, playback.ErrCapacity) {
			status = http.StatusTooManyRequests
		}
		http.Error(w, "No se pudo abrir la reproducción", status)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *server) playbackStatus(w http.ResponseWriter, r *http.Request) {
	view, ok := s.d.Playback.Status(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) pausePlayback(w http.ResponseWriter, r *http.Request) {
	s.playbackControl(w, r, s.d.Playback.Pause)
}

func (s *server) resumePlayback(w http.ResponseWriter, r *http.Request) {
	s.playbackControl(w, r, s.d.Playback.Resume)
}

func (s *server) playbackControl(w http.ResponseWriter, r *http.Request, control func(context.Context, string) error) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := control(ctx, r.PathValue("id")); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, playback.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, "No se pudo controlar la reproducción", status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) seekPlayback(w http.ResponseWriter, r *http.Request) {
	position, err := time.Parse(time.RFC3339, r.PostFormValue("position"))
	if err != nil {
		http.Error(w, "Posición no válida", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.d.Playback.Seek(ctx, r.PathValue("id"), position); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, playback.ErrSeekOutOfRange) {
			status = http.StatusBadRequest
		} else if errors.Is(err, playback.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, "No se pudo mover la reproducción", status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) closePlayback(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Playback.ClosePlayback(r.PathValue("id")); err != nil {
		http.Error(w, "No se pudo cerrar la reproducción", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
