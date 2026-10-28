package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/belcaik/open-nvr/internal/nvr"
)

func (s *server) registerSettingsRoutes(mux *http.ServeMux, authed func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /settings", authed(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/camera/"+strconv.Itoa(s.d.Channels[0]), http.StatusSeeOther)
	}))
	mux.Handle("GET /settings/camera/{ch}", authed(s.settingsCamera))
	mux.Handle("PUT /api/settings/{ch}/color", authed(s.putColor))
	mux.Handle("PUT /api/settings/{ch}/basic", authed(s.putBasic))
	mux.Handle("PUT /api/settings/{ch}/ircut", authed(s.putIrCut))
	mux.Handle("PUT /api/settings/{ch}/motion", authed(s.putMotion))
	mux.Handle("PUT /api/settings/{ch}/schedule", authed(s.putSchedule))
	mux.Handle("GET /settings/nvr", authed(s.settingsNVR))
	mux.Handle("PUT /api/settings/nvr/record", authed(s.putRecordSettings))
	mux.Handle("POST /api/settings/nvr/ntp-sync", authed(s.postNTPSync))
}

// section guarda el valor leído o el error de cada bloque: un bloque que
// falla no impide mostrar los demás.
type section[T any] struct {
	V   T
	Err string
}

func load[T any](ctx context.Context, f func(context.Context) (T, error)) section[T] {
	v, err := f(ctx)
	if err != nil {
		_, msg := nvrError(err)
		return section[T]{Err: msg}
	}
	return section[T]{V: v}
}

type scheduleView struct {
	nvr.RecordSchedule
	Mode string // continuous|off|custom
}

type cameraSettingsPage struct {
	Channel  int
	Channels []int
	Color    section[nvr.ImageColor]
	Basic    section[nvr.ImageBasic]
	IrCut    section[nvr.IrCut]
	Motion   section[nvr.Motion]
	Schedule section[scheduleView]
	Main     section[nvr.StreamInfo]
	Sub      section[nvr.StreamInfo]
}

func scheduleMode(rs nvr.RecordSchedule) string {
	on, off := 0, 0
	for _, d := range rs.Days {
		for _, b := range d {
			if b {
				on++
			} else {
				off++
			}
		}
	}
	switch {
	case off == 0:
		return "continuous"
	case on == 0:
		return "off"
	}
	return "custom"
}

func (s *server) settingsCamera(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c := s.d.Config
	p := cameraSettingsPage{Channel: ch, Channels: s.d.Channels}
	p.Color = load(ctx, func(ctx context.Context) (nvr.ImageColor, error) { return c.ImageColor(ctx, ch) })
	p.Basic = load(ctx, func(ctx context.Context) (nvr.ImageBasic, error) { return c.ImageBasic(ctx, ch) })
	p.IrCut = load(ctx, func(ctx context.Context) (nvr.IrCut, error) { return c.IrCut(ctx, ch) })
	p.Motion = load(ctx, func(ctx context.Context) (nvr.Motion, error) { return c.Motion(ctx, ch) })
	p.Schedule = load(ctx, func(ctx context.Context) (scheduleView, error) {
		rs, err := c.RecordSchedule(ctx, ch)
		return scheduleView{RecordSchedule: rs, Mode: scheduleMode(rs)}, err
	})
	p.Main = load(ctx, func(ctx context.Context) (nvr.StreamInfo, error) { return c.Stream(ctx, ch, 1) })
	p.Sub = load(ctx, func(ctx context.Context) (nvr.StreamInfo, error) { return c.Stream(ctx, ch, 2) })
	s.render(w, r, http.StatusOK, "settings_camera", "Ajustes · Cámara "+strconv.Itoa(ch), p)
}

// formInt lee un entero del formulario; ok=false si falta o no es número.
func formInt(r *http.Request, name string) (int, bool) {
	n, err := strconv.Atoi(r.PostFormValue(name))
	return n, err == nil
}

func (s *server) badForm(w http.ResponseWriter, r *http.Request, msg string) {
	s.render(w, r, http.StatusBadRequest, "_result", "", struct {
		OK  bool
		Msg string
	}{false, msg})
}

func (s *server) putColor(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var v nvr.ImageColor
	fields := []struct {
		name string
		dst  *int
	}{{"brightness", &v.Brightness}, {"contrast", &v.Contrast}, {"saturation", &v.Saturation},
		{"hue", &v.Hue}, {"sharpness", &v.Sharpness}}
	for _, f := range fields {
		n, ok := formInt(r, f.name)
		if !ok {
			s.badForm(w, r, "Valor no válido: "+f.name)
			return
		}
		*f.dst = n
	}
	s.result(w, r, s.d.Config.SetImageColor(r.Context(), ch, v), "Imagen guardada")
}

func (s *server) putBasic(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := nvr.ImageBasic{FlipMode: r.PostFormValue("flip"), WDR: r.PostFormValue("wdr"),
		NoiseReduce: r.PostFormValue("nr"), BLC: r.PostFormValue("blc")}
	s.result(w, r, s.d.Config.SetImageBasic(r.Context(), ch, v), "Ajustes básicos guardados")
}

func (s *server) putIrCut(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sens, okS := formInt(r, "sensitivity")
	if !okS {
		s.badForm(w, r, "Sensibilidad no válida")
		return
	}
	v := nvr.IrCut{Mode: r.PostFormValue("mode"), DayStart: withSeconds(r.PostFormValue("day_start")),
		DayEnd: withSeconds(r.PostFormValue("day_end")), Sensitivity: sens}
	s.result(w, r, s.d.Config.SetIrCut(r.Context(), ch, v), "Modo día/noche guardado")
}

// withSeconds convierte el "HH:MM" de <input type=time> en "HH:MM:SS".
func withSeconds(t string) string {
	if len(t) == 5 {
		return t + ":00"
	}
	return t
}

func (s *server) putMotion(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := nvr.Motion{Enabled: r.PostFormValue("enabled") == "on", Sensitivity: r.PostFormValue("sensitivity")}
	msg := "Detección de movimiento desactivada"
	if v.Enabled {
		msg = "Detección de movimiento activada"
	}
	s.result(w, r, s.d.Config.SetMotion(r.Context(), ch, v), msg)
}

func (s *server) putSchedule(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cur, err := s.d.Config.RecordSchedule(r.Context(), ch)
	if err != nil {
		s.result(w, r, err, "")
		return
	}
	var on bool
	switch r.PostFormValue("mode") {
	case "continuous":
		on = true
	case "off":
	default:
		s.badForm(w, r, "Modo de grabación no válido")
		return
	}
	cur.AllDay = on
	for d := range cur.Days {
		for h := range cur.Days[d] {
			cur.Days[d][h] = on
		}
	}
	msg := "Grabación continua activada"
	if !on {
		msg = "Grabación desactivada"
	}
	s.result(w, r, s.d.Config.SetRecordSchedule(r.Context(), ch, cur), msg)
}

type nvrSettingsPage struct {
	Clock   section[nvr.Clock]
	Disks   section[[]nvr.Disk]
	Network section[nvr.NetworkInfo]
	Record  section[nvr.RecordSettings]
}

func (s *server) settingsNVR(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c := s.d.Config
	p := nvrSettingsPage{
		Clock:   load(ctx, c.Clock),
		Disks:   load(ctx, c.Disks),
		Network: load(ctx, c.Network),
		Record:  load(ctx, c.RecordSettings),
	}
	s.render(w, r, http.StatusOK, "settings_nvr", "Ajustes · NVR", p)
}

func (s *server) putRecordSettings(w http.ResponseWriter, r *http.Request) {
	pre, ok1 := formInt(r, "pre")
	delay, ok2 := formInt(r, "delay")
	if !ok1 || !ok2 {
		s.badForm(w, r, "Valores no válidos")
		return
	}
	v := nvr.RecordSettings{PreRecord: pre, DelayRecord: delay, StreamType: r.PostFormValue("stream"),
		Overwrite: r.PostFormValue("overwrite") == "on"}
	s.result(w, r, s.d.Config.SetRecordSettings(r.Context(), v), "Ajustes de grabación guardados")
}

func (s *server) postNTPSync(w http.ResponseWriter, r *http.Request) {
	s.result(w, r, s.d.Config.SyncNTP(r.Context()), "Sincronización pedida")
}
