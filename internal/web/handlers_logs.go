package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

type logsQuery struct {
	Major string
	Start time.Time
	Stop  time.Time
}

type logsPage struct {
	Major   string
	Start   string
	Stop    string
	Entries []nvr.LogEntry
	Err     string
	CSVURL  string
}

func (s *server) parseLogsQuery(r *http.Request) (logsQuery, error) {
	now := time.Now().In(s.d.Location).Truncate(time.Minute)
	q := logsQuery{Major: r.URL.Query().Get("type"), Start: now.Add(-24 * time.Hour), Stop: now}
	if q.Major == "" {
		q.Major = "9"
	}
	parse := func(key string, fallback time.Time) (time.Time, error) {
		raw := r.URL.Query().Get(key)
		if raw == "" {
			return fallback, nil
		}
		return time.ParseInLocation("2006-01-02T15:04", raw, s.d.Location)
	}
	var err error
	if q.Start, err = parse("start", q.Start); err != nil {
		return logsQuery{}, err
	}
	if q.Stop, err = parse("stop", q.Stop); err != nil {
		return logsQuery{}, err
	}
	if q.Stop.Before(q.Start) || q.Stop.Sub(q.Start) > 31*24*time.Hour {
		return logsQuery{}, &nvr.ValidationError{Field: "rango", Reason: "debe estar ordenado y no superar 31 días"}
	}
	return q, nil
}

func (s *server) settingsLogs(w http.ResponseWriter, r *http.Request) {
	q, err := s.parseLogsQuery(r)
	if err != nil {
		s.badForm(w, r, "Rango de fechas no válido (máximo 31 días)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	entries, err := s.d.Config.Logs(ctx, q.Major, q.Start, q.Stop)
	p := logsPage{Major: q.Major, Start: q.Start.Format("2006-01-02T15:04"), Stop: q.Stop.Format("2006-01-02T15:04"), Entries: entries}
	params := url.Values{"type": {q.Major}, "start": {p.Start}, "stop": {p.Stop}}
	p.CSVURL = "/api/settings/logs.csv?" + params.Encode()
	if err != nil {
		_, p.Err = nvrError(err)
	}
	s.render(w, r, http.StatusOK, "logs", "Registros del NVR", p)
}

func (s *server) logsCSV(w http.ResponseWriter, r *http.Request) {
	q, err := s.parseLogsQuery(r)
	if err != nil {
		http.Error(w, "Rango de fechas no válido", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	entries, err := s.d.Config.Logs(ctx, q.Major, q.Start, q.Stop)
	if err != nil {
		status, msg := nvrError(err)
		http.Error(w, msg, status)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="registros-nvr.csv"`)
	w.Write([]byte{0xef, 0xbb, 0xbf})
	writeCSVRow(w, []string{"fecha", "usuario", "IP", "tipo", "canal", "MAC/conflicto"})
	for _, entry := range entries {
		writeCSVRow(w, []string{formatLogTime(entry.Time), entry.User, entry.HostIP, entry.MinorType, entry.Channel, entry.IPConflictMAC})
	}
}

func writeCSVRow(w http.ResponseWriter, fields []string) {
	for i, field := range fields {
		if i > 0 {
			_, _ = w.Write([]byte{','})
		}
		if strings.ContainsAny(field, ",\"\r\n") {
			field = `"` + strings.ReplaceAll(field, `"`, `""`) + `"`
		}
		_, _ = w.Write([]byte(field))
	}
	_, _ = w.Write([]byte{'\r', '\n'})
}

func formatLogTime(raw string) string {
	if len(raw) == 14 && strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return raw[0:4] + "-" + raw[4:6] + "-" + raw[6:8] + " " + raw[8:10] + ":" + raw[10:12] + ":" + raw[12:14]
	}
	return raw
}
