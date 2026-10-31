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
	mux.Handle("PUT /api/settings/{ch}/osd", authed(s.putOSD))
	mux.Handle("PUT /api/settings/{ch}/password", authed(s.putCameraPassword))
	mux.Handle("PUT /api/settings/{ch}/white-balance", authed(s.putWhiteBalance))
	mux.Handle("PUT /api/settings/{ch}/exposure", authed(s.putExposure))
	mux.Handle("PUT /api/settings/{ch}/scene", authed(s.putScene))
	mux.Handle("PUT /api/settings/{ch}/color", authed(s.putColor))
	mux.Handle("PUT /api/settings/{ch}/basic", authed(s.putBasic))
	mux.Handle("PUT /api/settings/{ch}/ircut", authed(s.putIrCut))
	mux.Handle("PUT /api/settings/{ch}/motion", authed(s.putMotion))
	mux.Handle("PUT /api/settings/{ch}/schedule", authed(s.putSchedule))
	mux.Handle("GET /settings/nvr", authed(s.settingsNVR))
	mux.Handle("PUT /api/settings/nvr/record", authed(s.putRecordSettings))
	mux.Handle("PUT /api/settings/nvr/p2p", authed(s.putP2P))
	mux.Handle("PUT /api/settings/nvr/audio", authed(s.putAudio))
	mux.Handle("POST /api/settings/nvr/ntp-sync", authed(s.postNTPSync))
	mux.Handle("GET /settings/logs", authed(s.settingsLogs))
	mux.Handle("GET /api/settings/logs.csv", authed(s.logsCSV))
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
	Channel      int
	Channels     []int
	OSD          section[nvr.OSD]
	WhiteBalance section[nvr.WhiteBalance]
	Exposure     section[nvr.Exposure]
	Scene        section[nvr.Scene]
	Color        section[nvr.ImageColor]
	Basic        section[nvr.ImageBasic]
	IrCut        section[nvr.IrCut]
	Motion       section[nvr.Motion]
	Schedule     section[scheduleView]
	Main         section[nvr.StreamInfo]
	Sub          section[nvr.StreamInfo]
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
	p.OSD = load(ctx, func(ctx context.Context) (nvr.OSD, error) { return c.OSD(ctx, ch) })
	p.WhiteBalance = load(ctx, func(ctx context.Context) (nvr.WhiteBalance, error) { return c.WhiteBalance(ctx, ch) })
	p.Exposure = load(ctx, func(ctx context.Context) (nvr.Exposure, error) { return c.Exposure(ctx, ch) })
	p.Scene = load(ctx, func(ctx context.Context) (nvr.Scene, error) { return c.Scene(ctx, ch) })
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

func (s *server) putOSD(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := nvr.OSD{NameEnabled: r.PostFormValue("name_enabled") == "on", Name: r.PostFormValue("name"), TimeEnabled: r.PostFormValue("time_enabled") == "on"}
	s.result(w, r, s.d.Config.SetOSD(r.Context(), ch, v), "OSD guardado")
}

func (s *server) putCameraPassword(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	password := r.PostFormValue("password")
	if password != r.PostFormValue("confirmation") {
		s.badForm(w, r, "Las contraseñas no coinciden")
		return
	}
	s.result(w, r, s.d.Config.SetCameraPassword(r.Context(), ch, password), "Contraseña cambiada y conexión verificada")
}

func (s *server) putWhiteBalance(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	red, okRed := formInt(r, "gain_red")
	blue, okBlue := formInt(r, "gain_blue")
	if !okRed || !okBlue {
		s.badForm(w, r, "Ganancia no válida")
		return
	}
	v := nvr.WhiteBalance{Mode: r.PostFormValue("mode"), GainRed: red, GainBlue: blue}
	s.result(w, r, s.d.Config.SetWhiteBalance(r.Context(), ch, v), "Balance de blancos guardado")
}

func (s *server) putExposure(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	v := nvr.Exposure{IrisType: r.PostFormValue("iris"), Shutter: r.PostFormValue("shutter"), Gain: r.PostFormValue("gain")}
	s.result(w, r, s.d.Config.SetExposure(r.Context(), ch, v), "Exposición guardada")
}

func (s *server) putScene(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.result(w, r, s.d.Config.SetScene(r.Context(), ch, nvr.Scene{FogValue: r.PostFormValue("fog")}), "Escena guardada")
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
	mode := r.PostFormValue("mode")
	var on bool
	switch mode {
	case "continuous":
		on = true
	case "off":
	case "custom":
	default:
		s.badForm(w, r, "Modo de grabación no válido")
		return
	}
	cur.AllDay = mode == "continuous"
	for d := range cur.Days {
		for h := range cur.Days[d] {
			if mode == "custom" {
				cur.Days[d][h] = r.PostFormValue("slot_"+strconv.Itoa(d)+"_"+strconv.Itoa(h)) == "on"
			} else {
				cur.Days[d][h] = on
			}
		}
	}
	msg := "Horario personalizado guardado"
	if mode == "continuous" {
		msg = "Grabación continua activada"
	}
	if mode == "off" {
		msg = "Grabación desactivada"
	}
	s.result(w, r, s.d.Config.SetRecordSchedule(r.Context(), ch, cur), msg)
}

type nvrSettingsPage struct {
	Clock   section[nvr.Clock]
	Disks   section[[]nvr.Disk]
	Network section[nvr.NetworkInfo]
	Record  section[nvr.RecordSettings]
	P2P     section[nvr.P2P]
	Audio   section[nvr.AudioStream]
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
		P2P:     load(ctx, c.P2P),
		Audio:   load(ctx, c.AudioStream),
	}
	s.render(w, r, http.StatusOK, "settings_nvr", "Ajustes · NVR", p)
}

func (s *server) putP2P(w http.ResponseWriter, r *http.Request) {
	enabled := r.PostFormValue("enabled") == "on"
	msg := "P2P desactivado"
	if enabled {
		msg = "P2P activado"
	}
	s.result(w, r, s.d.Config.SetP2P(r.Context(), enabled), msg)
}

func (s *server) putAudio(w http.ResponseWriter, r *http.Request) {
	v, err := s.d.Config.AudioStream(r.Context())
	if err != nil {
		s.result(w, r, err, "")
		return
	}
	in, okIn := formInt(r, "input_volume")
	out, okOut := formInt(r, "output_volume")
	if !okIn || (v.HasOutputVolume && !okOut) {
		s.badForm(w, r, "Volumen no válido")
		return
	}
	v.InputMethod = r.PostFormValue("input_method")
	v.InputVolume = in
	if v.HasOutputVolume {
		v.OutputVolume = out
	}
	s.result(w, r, s.d.Config.SetAudioStream(r.Context(), v), "Audio guardado")
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
