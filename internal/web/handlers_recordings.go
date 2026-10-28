package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
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

	p := recordingsPage{Channel: ch, Channels: s.d.Channels, Day: day,
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
		})
	}
	p.Total = fmt.Sprintf("%dh %02dm grabadas", int(total.Hours()), int(total.Minutes())%60)
	s.render(w, r, http.StatusOK, "recordings", "Grabaciones", p)
}
