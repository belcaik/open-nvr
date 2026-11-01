package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/playback"
)

type fakeConfig struct {
	calls          []string
	schedule       nvr.RecordSchedule
	err            error
	cameraPassword string
}

func (f *fakeConfig) rec(format string, a ...any) error {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
}
func (f *fakeConfig) OSD(context.Context, int) (nvr.OSD, error) {
	return nvr.OSD{NameEnabled: true, Name: "Patio", TimeEnabled: true}, f.err
}
func (f *fakeConfig) SetOSD(_ context.Context, ch int, v nvr.OSD) error {
	return f.rec("osd %d %+v", ch, v)
}
func (f *fakeConfig) SetCameraPassword(_ context.Context, ch int, password string) error {
	f.cameraPassword = password
	return f.rec("camera password %d", ch)
}
func (f *fakeConfig) WhiteBalance(context.Context, int) (nvr.WhiteBalance, error) {
	return nvr.WhiteBalance{Mode: "auto", GainRed: 50, GainBlue: 50}, f.err
}
func (f *fakeConfig) SetWhiteBalance(_ context.Context, ch int, v nvr.WhiteBalance) error {
	return f.rec("wb %d %+v", ch, v)
}
func (f *fakeConfig) Exposure(context.Context, int) (nvr.Exposure, error) {
	return nvr.Exposure{IrisType: "manual", Shutter: "auto", Gain: "auto"}, f.err
}
func (f *fakeConfig) SetExposure(_ context.Context, ch int, v nvr.Exposure) error {
	return f.rec("exposure %d %+v", ch, v)
}
func (f *fakeConfig) Scene(context.Context, int) (nvr.Scene, error) {
	return nvr.Scene{FogValue: "30"}, f.err
}
func (f *fakeConfig) SetScene(_ context.Context, ch int, v nvr.Scene) error {
	return f.rec("scene %d %+v", ch, v)
}
func (f *fakeConfig) ImageColor(context.Context, int) (nvr.ImageColor, error) {
	return nvr.ImageColor{Brightness: 50, Contrast: 51, Saturation: 52, Hue: 53, Sharpness: 54}, f.err
}
func (f *fakeConfig) SetImageColor(_ context.Context, ch int, v nvr.ImageColor) error {
	return f.rec("color %d %+v", ch, v)
}
func (f *fakeConfig) ImageBasic(context.Context, int) (nvr.ImageBasic, error) {
	return nvr.ImageBasic{FlipMode: "close", WDR: "automatic", NoiseReduce: "close", BLC: "close"}, f.err
}
func (f *fakeConfig) SetImageBasic(_ context.Context, ch int, v nvr.ImageBasic) error {
	return f.rec("basic %d %+v", ch, v)
}
func (f *fakeConfig) IrCut(context.Context, int) (nvr.IrCut, error) {
	return nvr.IrCut{Mode: "intelligent", DayStart: "07:00:00", DayEnd: "19:00:00", Sensitivity: 45}, f.err
}
func (f *fakeConfig) SetIrCut(_ context.Context, ch int, v nvr.IrCut) error {
	return f.rec("ircut %d %+v", ch, v)
}
func (f *fakeConfig) Motion(context.Context, int) (nvr.Motion, error) {
	return nvr.Motion{Sensitivity: "middle"}, f.err
}
func (f *fakeConfig) SetMotion(_ context.Context, ch int, v nvr.Motion) error {
	return f.rec("motion %d %+v", ch, v)
}
func (f *fakeConfig) RecordSchedule(context.Context, int) (nvr.RecordSchedule, error) {
	return f.schedule, f.err
}
func (f *fakeConfig) SetRecordSchedule(_ context.Context, ch int, v nvr.RecordSchedule) error {
	on := 0
	for _, d := range v.Days {
		for _, b := range d {
			if b {
				on++
			}
		}
	}
	return f.rec("schedule %d allday=%v on=%d", ch, v.AllDay, on)
}
func (f *fakeConfig) Stream(_ context.Context, ch, stream int) (nvr.StreamInfo, error) {
	return nvr.StreamInfo{Codec: "H.265", Width: 2304, Height: 1296, FPS: 10}, f.err
}
func (f *fakeConfig) RecordSettings(context.Context) (nvr.RecordSettings, error) {
	return nvr.RecordSettings{PreRecord: 0, DelayRecord: 30, StreamType: "main", Overwrite: true}, f.err
}
func (f *fakeConfig) SetRecordSettings(_ context.Context, v nvr.RecordSettings) error {
	return f.rec("record %+v", v)
}
func (f *fakeConfig) Clock(context.Context) (nvr.Clock, error) {
	return nvr.Clock{Now: time.Date(2026, 10, 28, 1, 8, 3, 0, time.UTC), NTPEnabled: true, NTPServer: "192.168.0.8"}, f.err
}
func (f *fakeConfig) SyncNTP(context.Context) error { return f.rec("ntp") }
func (f *fakeConfig) Disks(context.Context) ([]nvr.Disk, error) {
	return []nvr.Disk{{Model: "ST3500418AS", TotalMB: 476940, FreeMB: 464150, State: "writting"}}, f.err
}
func (f *fakeConfig) Network(context.Context) (nvr.NetworkInfo, error) {
	return nvr.NetworkInfo{IP: "192.168.0.227", Gateway: "192.168.0.8"}, f.err
}
func (f *fakeConfig) P2P(context.Context) (nvr.P2P, error) {
	return nvr.P2P{Enabled: true, UUID: "TEST-UUID", State: "internet-ready"}, f.err
}
func (f *fakeConfig) SetP2P(_ context.Context, enabled bool) error { return f.rec("p2p %v", enabled) }
func (f *fakeConfig) AudioStream(context.Context) (nvr.AudioStream, error) {
	return nvr.AudioStream{InputMethod: "microphone", Codec: "G.711U", InputVolume: 55, HasOutputVolume: true, OutputVolume: 65}, f.err
}
func (f *fakeConfig) SetAudioStream(_ context.Context, v nvr.AudioStream) error {
	return f.rec("audio %+v", v)
}
func (f *fakeConfig) Logs(_ context.Context, major string, start, stop time.Time) ([]nvr.LogEntry, error) {
	f.calls = append(f.calls, fmt.Sprintf("logs %s %s %s", major, start.Format(time.RFC3339), stop.Format(time.RFC3339)))
	return []nvr.LogEntry{{Time: "20261030124530", User: "admin", HostIP: "192.0.2.10", MinorType: "304", Channel: "-1"}}, f.err
}

type fakeRecordings struct{ loc *time.Location }

func (f *fakeRecordings) RecordDays(context.Context, int, time.Month) ([]int, error) {
	return []int{27, 28}, nil
}
func (f *fakeRecordings) RecordSegments(_ context.Context, ch int, day time.Time) ([]nvr.Segment, error) {
	d := func(h, m int) time.Time { return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, f.loc) }
	return []nvr.Segment{{Start: d(0, 0), End: d(6, 0), Type: "general"}, {Start: d(12, 0), End: d(12, 30), Type: "motion"}}, nil
}

type fakeHub struct{ ch chan nvr.Event }

func (h *fakeHub) Subscribe() (<-chan nvr.Event, func()) { return h.ch, func() {} }

type fakePlayback struct {
	calls []string
	view  playback.View
}

func (f *fakePlayback) Open(_ context.Context, r playback.Request) (playback.View, error) {
	f.calls = append(f.calls, fmt.Sprintf("open %d %s %s", r.Channel, r.Start.Format(time.RFC3339), r.End.Format(time.RFC3339)))
	f.view = playback.View{ID: "opaque-id", Stream: "playback_0123456789abcdef0123456789abcdef", State: playback.StateReady,
		Channel: r.Channel, Start: r.Start, End: r.End}
	return f.view, nil
}
func (f *fakePlayback) Status(id string) (playback.View, bool) {
	f.calls = append(f.calls, "status "+id)
	return f.view, f.view.ID == id
}
func (f *fakePlayback) Pause(_ context.Context, id string) error {
	f.calls = append(f.calls, "pause "+id)
	return nil
}
func (f *fakePlayback) Resume(_ context.Context, id string) error {
	f.calls = append(f.calls, "resume "+id)
	return nil
}
func (f *fakePlayback) Seek(_ context.Context, id string, at time.Time) error {
	f.calls = append(f.calls, "seek "+id+" "+at.Format(time.RFC3339))
	return nil
}
func (f *fakePlayback) ClosePlayback(id string) error {
	f.calls = append(f.calls, "close "+id)
	return nil
}

func newFullEnv(t *testing.T) (*env, *fakeConfig, *fakeHub) {
	t.Helper()
	e := newEnv(t)
	cfg := &fakeConfig{}
	cfg.schedule.StreamType = "main"
	for d := range cfg.schedule.Days {
		for h := range cfg.schedule.Days[d] {
			cfg.schedule.Days[d][h] = true
		}
	}
	hub := &fakeHub{ch: make(chan nvr.Event, 1)}
	play := &fakePlayback{}
	e.play = play
	loc, _ := time.LoadLocation("America/Santiago")
	e.h = NewServer(Deps{
		Cameras: e.cams, Sessions: e.sess, Limiter: auth.NewLimiter(5, time.Minute, time.Now),
		PassHash: testHash, User: "admin", Channels: []int{1, 2, 3, 4},
		Config: cfg, Recordings: &fakeRecordings{loc: loc}, Playback: play,
		PlaybackSource: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "source %s", r.PathValue("token"))
		}), Events: hub, Location: loc,
	})
	return e, cfg, hub
}

func TestSettingsCameraPage(t *testing.T) {
	e, _, _ := newFullEnv(t)
	body := e.do(e.authed(t, "GET", "/settings/camera/2", nil)).Body.String()
	for _, want := range []string{`hx-put="/api/settings/2/osd"`, `value="Patio"`, `hx-put="/api/settings/2/white-balance"`,
		`hx-put="/api/settings/2/exposure"`, `hx-put="/api/settings/2/scene"`, `hx-put="/api/settings/2/color"`, `value="53"`, "leftright", "variablewhitelight",
		"continuo (24 h, todos los días)", `name="slot_6_47"`, "H.265 2304×1296",
		`hx-put="/api/settings/2/password"`, `data-reset-on-success`, `autocomplete="new-password"`, "forms.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q", want)
		}
	}
}

func TestSettingsSectionErrorDoesNotBreakPage(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	cfg.err = nvr.ErrUnavailable
	rec := e.do(e.authed(t, "GET", "/settings/camera/1", nil))
	if rec.Code != 200 || strings.Count(rec.Body.String(), "El NVR no responde") < 5 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPutColor(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	v := url.Values{"brightness": {"55"}, "contrast": {"50"}, "saturation": {"50"}, "hue": {"50"}, "sharpness": {"50"}}
	rec := e.do(e.authed(t, "PUT", "/api/settings/1/color", v))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Imagen guardada") || !strings.Contains(cfg.calls[0], "Brightness:55") {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, cfg.calls)
	}
	v.Set("hue", "x")
	if rec := e.do(e.authed(t, "PUT", "/api/settings/1/color", v)); rec.Code != http.StatusBadRequest {
		t.Fatal(rec.Code)
	}
}

func TestValidationErrorIs400(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	cfg.err = &nvr.ValidationError{Field: "Brightness", Reason: "fuera de rango"}
	v := url.Values{"brightness": {"500"}, "contrast": {"50"}, "saturation": {"50"}, "hue": {"50"}, "sharpness": {"50"}}
	rec := e.do(e.authed(t, "PUT", "/api/settings/1/color", v))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Brightness") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPutMotionAndIrCut(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	e.do(e.authed(t, "PUT", "/api/settings/1/motion", url.Values{"enabled": {"on"}, "sensitivity": {"high"}}))
	e.do(e.authed(t, "PUT", "/api/settings/1/ircut", url.Values{"mode": {"intelligent"}, "day_start": {"07:00"}, "day_end": {"19:30:00"}, "sensitivity": {"40"}}))
	if !strings.Contains(cfg.calls[0], "Enabled:true") || !strings.Contains(cfg.calls[1], "DayStart:07:00:00") || !strings.Contains(cfg.calls[1], "DayEnd:19:30:00") {
		t.Fatal(cfg.calls)
	}
}

func TestPutSchedule(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	e.do(e.authed(t, "PUT", "/api/settings/3/schedule", url.Values{"mode": {"off"}}))
	if cfg.calls[0] != "schedule 3 allday=false on=0" {
		t.Fatal(cfg.calls)
	}
	if rec := e.do(e.authed(t, "PUT", "/api/settings/3/schedule", url.Values{"mode": {"x"}})); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	cfg.calls = nil
	e.do(e.authed(t, "PUT", "/api/settings/3/schedule", url.Values{"mode": {"custom"}, "slot_0_0": {"on"}, "slot_6_47": {"on"}}))
	if cfg.calls[0] != "schedule 3 allday=false on=2" || cfg.schedule.StreamType != "main" {
		t.Fatal(cfg.calls)
	}
}

func TestSettingsWritesRequireCSRF(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	req := e.authed(t, "POST", "/api/settings/nvr/ntp-sync", nil)
	req.Header.Del("X-CSRF-Token")
	if rec := e.do(req); rec.Code != http.StatusForbidden || len(cfg.calls) != 0 {
		t.Fatal(rec.Code)
	}
}

func TestSettingsNVRPage(t *testing.T) {
	e, _, _ := newFullEnv(t)
	body := e.do(e.authed(t, "GET", "/settings/nvr", nil)).Body.String()
	for _, want := range []string{"ST3500418AS", "464150 de 476940", "192.168.0.227", "Sincronizar ahora", "TEST-UUID", "G.711U", `name="delay" min="0" max="300" value="30"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q", want)
		}
	}
}

func TestPutOSDImageAudioAndP2P(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	e.do(e.authed(t, "PUT", "/api/settings/2/osd", url.Values{"name_enabled": {"on"}, "name": {"Entrada"}, "time_enabled": {"on"}}))
	e.do(e.authed(t, "PUT", "/api/settings/2/white-balance", url.Values{"mode": {"manual"}, "gain_red": {"60"}, "gain_blue": {"40"}}))
	e.do(e.authed(t, "PUT", "/api/settings/2/exposure", url.Values{"iris": {"DC-Iris"}, "shutter": {"1/100"}, "gain": {"12dB"}}))
	e.do(e.authed(t, "PUT", "/api/settings/2/scene", url.Values{"fog": {"90"}}))
	e.do(e.authed(t, "PUT", "/api/settings/nvr/audio", url.Values{"input_method": {"line-in"}, "codec": {"G.711A"}, "input_volume": {"45"}, "output_volume": {"75"}}))
	e.do(e.authed(t, "PUT", "/api/settings/nvr/p2p", url.Values{}))
	want := []string{"osd 2", "wb 2", "exposure 2", "scene 2", "audio", "p2p false"}
	for i, part := range want {
		if !strings.Contains(cfg.calls[i], part) {
			t.Fatalf("calls = %v", cfg.calls)
		}
	}
	if !strings.Contains(cfg.calls[4], "G.711U") || strings.Contains(cfg.calls[4], "G.711A") {
		t.Fatalf("el formulario cambió el códec del talk: %v", cfg.calls[4])
	}
}

func TestPutCameraPassword(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	rec := e.do(e.authed(t, "PUT", "/api/settings/2/password", url.Values{"password": {"Clave2026segura"}, "confirmation": {"Clave2026segura"}}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Contraseña cambiada") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(cfg.calls) != 1 || cfg.calls[0] != "camera password 2" {
		t.Fatalf("calls = %v", cfg.calls)
	}
	if cfg.cameraPassword != "Clave2026segura" {
		t.Fatal("el handler no reenvió la contraseña exacta")
	}
	if strings.Contains(rec.Body.String(), "Clave2026segura") {
		t.Fatal("la respuesta contiene la contraseña")
	}
}

func TestPutCameraPasswordReportsRollbackOutcome(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&nvr.CameraPasswordError{Restored: true, Err: nvr.ErrUnavailable}, "se restauró la anterior"},
		{&nvr.CameraPasswordError{Restored: false, Err: nvr.ErrUnavailable}, "revisa la cámara"},
		{&nvr.ValidationError{Field: "Password", Reason: "debe tener entre 8 y 16 caracteres"}, "entre 8 y 16"},
	}
	for _, tc := range cases {
		e, cfg, _ := newFullEnv(t)
		cfg.err = tc.err
		rec := e.do(e.authed(t, "PUT", "/api/settings/2/password", url.Values{"password": {"Clave2026segura"}, "confirmation": {"Clave2026segura"}}))
		if rec.Code < 400 || !strings.Contains(rec.Body.String(), tc.want) || strings.Contains(rec.Body.String(), "Clave2026segura") {
			t.Fatalf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
}

func TestPutAudioReportsChangeIgnoredByNVR(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	cfg.err = &nvr.NotAppliedError{Path: "/Streams/AudioStream"}
	rec := e.do(e.authed(t, "PUT", "/api/settings/nvr/audio", url.Values{"input_method": {"line-in"}, "codec": {"G.711U"}, "input_volume": {"40"}}))
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "no aplicó el cambio") || strings.Contains(rec.Body.String(), "Audio guardado") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestPutCameraPasswordMismatchDoesNotWrite(t *testing.T) {
	e, cfg, _ := newFullEnv(t)
	rec := e.do(e.authed(t, "PUT", "/api/settings/2/password", url.Values{"password": {"Clave2026segura"}, "confirmation": {"otra2026"}}))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "no coinciden") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(cfg.calls) != 0 {
		t.Fatalf("calls = %v", cfg.calls)
	}
}

func TestLogsPageAndCSV(t *testing.T) {
	e, _, _ := newFullEnv(t)
	path := "/settings/logs?type=4&start=2026-10-30T00%3A00&stop=2026-10-31T00%3A00"
	rec := e.do(e.authed(t, "GET", path, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "2026-10-30 12:45:30") || !strings.Contains(rec.Body.String(), "Exportar CSV") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do(e.authed(t, "GET", "/api/settings/logs.csv?type=4&start=2026-10-30T00%3A00&stop=2026-10-31T00%3A00", nil))
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Body.String(), "admin,192.0.2.10,304") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	bad := e.do(e.authed(t, "GET", "/settings/logs?start=2026-01-01T00%3A00&stop=2026-03-01T00%3A00", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatal(bad.Code)
	}
}

func TestRecordingsPage(t *testing.T) {
	e, _, _ := newFullEnv(t)
	rec := e.do(e.authed(t, "GET", "/recordings?ch=2&date=2026-10-28", nil))
	body := rec.Body.String()
	for _, want := range []string{"octubre de 2026", `class="has sel" href="/recordings?ch=2&amp;date=2026-10-28"`,
		`x="0.0" y="0" width="360.0"`, `x="720.0" y="0" width="30.0"`, "6h 30m grabadas",
		`class="play-recording"`, `data-start="2026-10-28T00:00:00-03:00"`, "/static/js/recordings.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en\n%s", want, body)
		}
	}
	if strings.Contains(body, "Reproducción pendiente") || strings.Contains(strings.ToLower(body), "descargar") {
		t.Fatal("la plantilla anuncia funciones no sustentadas")
	}
	if rec := e.do(e.authed(t, "GET", "/recordings?date=ayer", nil)); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestPlaybackHTTPControlsRealSession(t *testing.T) {
	e, _, _ := newFullEnv(t)
	start := "2026-10-28T12:00:00-03:00"
	end := "2026-10-28T12:30:00-03:00"
	rec := e.do(e.authed(t, http.MethodPost, "/api/playbacks", url.Values{"channel": {"2"}, "start": {start}, "end": {end}}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("open = %d %s", rec.Code, rec.Body)
	}
	var view playback.View
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.ID != "opaque-id" || view.Stream != "playback_0123456789abcdef0123456789abcdef" {
		t.Fatalf("view = %+v, err = %v", view, err)
	}
	for _, tc := range []struct {
		method, path string
		form         url.Values
	}{
		{http.MethodPost, "/api/playbacks/opaque-id/pause", nil},
		{http.MethodPost, "/api/playbacks/opaque-id/resume", nil},
		{http.MethodPost, "/api/playbacks/opaque-id/seek", url.Values{"position": {"2026-10-28T12:10:00-03:00"}}},
		{http.MethodDelete, "/api/playbacks/opaque-id", nil},
	} {
		if got := e.do(e.authed(t, tc.method, tc.path, tc.form)); got.Code != http.StatusNoContent {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, got.Code, got.Body)
		}
	}
	want := []string{
		"open 2 2026-10-28T12:00:00-03:00 2026-10-28T12:30:00-03:00",
		"pause opaque-id", "resume opaque-id", "seek opaque-id 2026-10-28T12:10:00-03:00", "close opaque-id",
	}
	if got := e.play.(*fakePlayback).calls; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestPlaybackHTTPValidatesInputAndCSRF(t *testing.T) {
	e, _, _ := newFullEnv(t)
	req := e.authed(t, http.MethodPost, "/api/playbacks", url.Values{
		"channel": {"2"}, "start": {"2026-10-28T12:00:00-03:00"}, "end": {"2026-10-28T12:30:00-03:00"},
	})
	req.Header.Del("X-CSRF-Token")
	if rec := e.do(req); rec.Code != http.StatusForbidden {
		t.Fatalf("sin CSRF = %d", rec.Code)
	}
	bad := e.authed(t, http.MethodPost, "/api/playbacks", url.Values{
		"channel": {"2"}, "start": {"ayer"}, "end": {"2026-10-28T12:30:00-03:00"},
	})
	if rec := e.do(bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("fecha inválida = %d", rec.Code)
	}
	if got := e.play.(*fakePlayback).calls; len(got) != 0 {
		t.Fatalf("el servicio recibió input rechazado: %v", got)
	}
}

func TestPlaybackSourceUsesOpaqueTokenWithoutWebCookie(t *testing.T) {
	e, _, _ := newFullEnv(t)
	rec := e.do(httptest.NewRequest(http.MethodGet, "/internal/playback/opaque-source-token", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "source opaque-source-token" {
		t.Fatalf("source = %d %q", rec.Code, rec.Body.String())
	}
}

func TestEventsSSE(t *testing.T) {
	e, _, hub := newFullEnv(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	_, ck := e.session(t)
	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.AddCookie(ck)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(resp.Header)
	}
	hub.ch <- nvr.Event{Channel: 1, Kind: "motion", Active: true, Time: time.Now()}
	buf := make([]byte, 512)
	var got string
	for !strings.Contains(got, "data:") {
		n, err := resp.Body.Read(buf)
		if err != nil {
			t.Fatal(errors.Join(err, errors.New(got)))
		}
		got += string(buf[:n])
	}
	if !strings.Contains(got, `"channel":1`) || !strings.Contains(got, `"kind":"motion"`) {
		t.Fatal(got)
	}
}
