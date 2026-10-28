package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/auth"
	"github.com/belcaik/open-nvr/internal/nvr"
)

type fakeConfig struct {
	calls    []string
	schedule nvr.RecordSchedule
	err      error
}

func (f *fakeConfig) rec(format string, a ...any) error {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
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

func newFullEnv(t *testing.T) (*env, *fakeConfig, *fakeHub) {
	t.Helper()
	e := newEnv(t)
	cfg := &fakeConfig{}
	for d := range cfg.schedule.Days {
		for h := range cfg.schedule.Days[d] {
			cfg.schedule.Days[d][h] = true
		}
	}
	hub := &fakeHub{ch: make(chan nvr.Event, 1)}
	loc, _ := time.LoadLocation("America/Santiago")
	e.h = NewServer(Deps{
		Cameras: e.cams, Sessions: e.sess, Limiter: auth.NewLimiter(5, time.Minute, time.Now),
		PassHash: testHash, User: "admin", Channels: []int{1, 2, 3, 4},
		Config: cfg, Recordings: &fakeRecordings{loc: loc}, Events: hub, Location: loc,
	})
	return e, cfg, hub
}

func TestSettingsCameraPage(t *testing.T) {
	e, _, _ := newFullEnv(t)
	body := e.do(e.authed(t, "GET", "/settings/camera/2", nil)).Body.String()
	for _, want := range []string{`hx-put="/api/settings/2/color"`, `value="53"`, "leftright", "variablewhitelight",
		"continuo (24 h, todos los días)", "H.265 2304×1296"} {
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
	for _, want := range []string{"ST3500418AS", "464150 de 476940", "192.168.0.227", "Sincronizar ahora", `name="delay" min="0" max="300" value="30"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q", want)
		}
	}
}

func TestRecordingsPage(t *testing.T) {
	e, _, _ := newFullEnv(t)
	rec := e.do(e.authed(t, "GET", "/recordings?ch=2&date=2026-10-28", nil))
	body := rec.Body.String()
	for _, want := range []string{"octubre de 2026", `class="has sel" href="/recordings?ch=2&amp;date=2026-10-28"`,
		`x="0.0" y="0" width="360.0"`, `x="720.0" y="0" width="30.0"`, "6h 30m grabadas", "Reproducción pendiente"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en\n%s", want, body)
		}
	}
	if rec := e.do(e.authed(t, "GET", "/recordings?date=ayer", nil)); rec.Code != 400 {
		t.Fatal(rec.Code)
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
