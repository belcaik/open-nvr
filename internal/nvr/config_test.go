package nvr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func configFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixtureDir + "/config/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertLastPUT(t *testing.T, f *fakeNVR, path, fixture string, changes map[string]string) {
	t.Helper()
	want, err := PatchXML(configFixture(t, fixture), changes)
	if err != nil {
		t.Fatal(err)
	}
	got := []byte(f.last(t, "PUT", path).body)
	if !bytes.Equal(got, want) {
		t.Fatalf("PUT %s =\n%s\nquiero\n%s", path, got, want)
	}
}

func TestConfigurationGETs(t *testing.T) {
	f, c := newFakeNVR(t)
	ctx := context.Background()

	color, err := c.ImageColor(ctx, 1)
	if err != nil || color != (ImageColor{50, 50, 50, 50, 50}) {
		t.Fatalf("ImageColor = %+v, %v", color, err)
	}
	basic, err := c.ImageBasic(ctx, 1)
	if err != nil || basic != (ImageBasic{FlipMode: "close", WDR: "automatic", NoiseReduce: "close", BLC: "close"}) {
		t.Fatalf("ImageBasic = %+v, %v", basic, err)
	}
	ir, err := c.IrCut(ctx, 1)
	if err != nil || ir != (IrCut{Mode: "intelligent", DayStart: "07:00:00", DayEnd: "19:00:00", Sensitivity: 45}) {
		t.Fatalf("IrCut = %+v, %v", ir, err)
	}
	schedule, err := c.RecordSchedule(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.StreamType != "main" || !schedule.AllDay {
		t.Fatalf("RecordSchedule = %+v", schedule)
	}
	for day := range schedule.Days {
		for halfHour, enabled := range schedule.Days[day] {
			if !enabled {
				t.Fatalf("Days[%d][%d] = false", day, halfHour)
			}
		}
	}
	record, err := c.RecordSettings(ctx)
	if err != nil || record != (RecordSettings{PreRecord: 0, DelayRecord: 30, StreamType: "main", Overwrite: true}) {
		t.Fatalf("RecordSettings = %+v, %v", record, err)
	}

	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	if c.WithLocation(loc) != c {
		t.Fatal("WithLocation no devolvió el cliente")
	}
	clock, err := c.Clock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantTime := time.Date(2026, 10, 28, 1, 8, 3, 0, loc)
	if !clock.Now.Equal(wantTime) || clock.Now.Location() != loc || !clock.NTPEnabled || clock.NTPServer != "192.168.0.8" || clock.TimeZone != "CST--3:00:00" || clock.LastSync != "2026/10/28 00:00:01" {
		t.Fatalf("Clock = %+v, quiero Now=%v", clock, wantTime)
	}

	disks, err := c.Disks(ctx)
	if err != nil || len(disks) != 1 || disks[0] != (Disk{ID: 0, Model: "ST3500418AS", TotalMB: 476940, FreeMB: 464150, State: "writting"}) {
		t.Fatalf("Disks = %+v, %v", disks, err)
	}
	network, err := c.Network(ctx)
	wantNetwork := NetworkInfo{IP: "192.168.0.227", Mask: "255.255.255.0", Gateway: "192.168.0.8", DNS1: "192.168.0.8", DNS2: "192.168.0.8", MAC: "00:11:22:33:44:05", DHCP: false}
	if err != nil || network != wantNetwork {
		t.Fatalf("Network = %+v, %v", network, err)
	}
	motion, err := c.Motion(ctx, 1)
	if err != nil || motion != (Motion{Enabled: false, Sensitivity: "middle"}) {
		t.Fatalf("Motion = %+v, %v", motion, err)
	}
	stream, err := c.Stream(ctx, 1, 1)
	if err != nil || stream != (StreamInfo{Codec: "H.265", Width: 2304, Height: 1296, FPS: 10, BitrateKbps: 1024, BitrateType: "VBR"}) {
		t.Fatalf("Stream = %+v, %v", stream, err)
	}
	if got := f.last(t, "GET", "/Streams/1/1").uri; got != "/Streams/1/1" {
		t.Fatalf("ruta Stream = %q", got)
	}
}

func TestConfigurationSETsPatchCurrentXML(t *testing.T) {
	f, c := newFakeNVR(t)
	ctx := context.Background()

	if err := c.SetImageColor(ctx, 1, ImageColor{51, 52, 53, 54, 55}); err != nil {
		t.Fatal(err)
	}
	assertLastPUT(t, f, "/Images/1/Color", "Images_1_Color.xml", map[string]string{
		"Brightness": "51", "Contrast": "52", "Saturation": "53", "Hue": "54", "Sharpness": "55",
	})
	if err := c.SetImageBasic(ctx, 1, ImageBasic{FlipMode: "leftright", WDR: "strong", NoiseReduce: "normal", BLC: "open"}); err != nil {
		t.Fatal(err)
	}
	assertLastPUT(t, f, "/Images/1/Basic", "Images_1_Basic.xml", map[string]string{
		"FlipMode": "leftright", "WDR": "strong", "NoiseReduce": "normal", "BLC": "open",
	})
	if err := c.SetIrCut(ctx, 1, IrCut{Mode: "time", DayStart: "08:15:00", DayEnd: "18:45:59", Sensitivity: 60}); err != nil {
		t.Fatal(err)
	}
	assertLastPUT(t, f, "/Images/1/IrCutFilter", "Images_1_IrCutFilter.xml", map[string]string{
		"Mode": "time", "DayStartTime": "08:15:00", "DayEndTime": "18:45:59", "Sensitivity": "60",
	})

	var days [7][48]bool
	for day := range days {
		for halfHour := range days[day] {
			days[day][halfHour] = (day+halfHour)%2 == 0
		}
	}
	if err := c.SetRecordSchedule(ctx, 1, RecordSchedule{StreamType: "sub", AllDay: false, Days: days}); err != nil {
		t.Fatal(err)
	}
	scheduleChanges := map[string]string{"StreamType": "sub", "Schedule/AllDay": "false"}
	for day := range days {
		var value strings.Builder
		for _, enabled := range days[day] {
			if enabled {
				value.WriteByte('1')
			} else {
				value.WriteByte('0')
			}
		}
		scheduleChanges[fmt.Sprintf("Schedule/TimeBlockList/TimeBlock_%d", day)] = value.String()
	}
	assertLastPUT(t, f, "/Record/1/RecordSchedule", "Record_1_RecordSchedule.xml", scheduleChanges)

	if err := c.SetRecordSettings(ctx, RecordSettings{PreRecord: 5, DelayRecord: 90, StreamType: "sub", Overwrite: false}); err != nil {
		t.Fatal(err)
	}
	assertLastPUT(t, f, "/Record", "Record.xml", map[string]string{
		"PreRecord": "5", "DelayRecord": "90", "RecordStreamType": "sub", "RecordWriteCircleType": "fullnotwrite",
	})
	if err := c.SetMotion(ctx, 1, Motion{Enabled: true, Sensitivity: "high"}); err != nil {
		t.Fatal(err)
	}
	assertLastPUT(t, f, "/Pictures/1/Motion", "Pictures_1_Motion.xml", map[string]string{
		"Enable": "true", "Senstive": "high",
	})
}

func TestSyncNTPPatchesCurrentXML(t *testing.T) {
	f, c := newFakeNVR(t)
	if err := c.SyncNTP(context.Background()); err != nil {
		t.Fatal(err)
	}
	doc := configFixture(t, "System_NTP.xml")
	want := bytes.Replace(doc,
		[]byte("    <SynchronizeNTPNowStatus>"),
		[]byte("    <IsSynchronizeNTPNow>true</IsSynchronizeNTPNow>\n    <SynchronizeNTPNowStatus>"), 1)
	if got := []byte(f.last(t, "PUT", "/System/NTP").body); !bytes.Equal(got, want) {
		t.Fatalf("PUT /System/NTP =\n%s\nquiero\n%s", got, want)
	}
}

func TestConfigurationValidationMakesNoRequest(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"color alto", func(c *Client) error { return c.SetImageColor(context.Background(), 1, ImageColor{Brightness: 101}) }},
		{"color negativo", func(c *Client) error { return c.SetImageColor(context.Background(), 1, ImageColor{Contrast: -1}) }},
		{"giro desconocido", func(c *Client) error {
			return c.SetImageBasic(context.Background(), 1, ImageBasic{FlipMode: "diagonal", WDR: "close", NoiseReduce: "close", BLC: "open"})
		}},
		{"WDR desconocido", func(c *Client) error {
			return c.SetImageBasic(context.Background(), 1, ImageBasic{FlipMode: "close", WDR: "yes", NoiseReduce: "close", BLC: "open"})
		}},
		{"hora IR inválida", func(c *Client) error {
			return c.SetIrCut(context.Background(), 1, IrCut{Mode: "intelligent", DayStart: "25:00:00", DayEnd: "19:00:00", Sensitivity: 50})
		}},
		{"modo IR desconocido", func(c *Client) error {
			return c.SetIrCut(context.Background(), 1, IrCut{Mode: "moon", DayStart: "07:00:00", DayEnd: "19:00:00", Sensitivity: 50})
		}},
		{"stream horario desconocido", func(c *Client) error {
			return c.SetRecordSchedule(context.Background(), 1, RecordSchedule{StreamType: "third"})
		}},
		{"pregrabación negativa", func(c *Client) error {
			return c.SetRecordSettings(context.Background(), RecordSettings{PreRecord: -1, StreamType: "main"})
		}},
		{"retardo alto", func(c *Client) error {
			return c.SetRecordSettings(context.Background(), RecordSettings{DelayRecord: 301, StreamType: "main"})
		}},
		{"sensibilidad movimiento desconocida", func(c *Client) error { return c.SetMotion(context.Background(), 1, Motion{Sensitivity: "extreme"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := newFakeNVR(t)
			err := tt.call(c)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v (%T), quiero *ValidationError", err, err)
			}
			if f.count() != 0 {
				t.Fatalf("se hicieron %d peticiones con datos inválidos", f.count())
			}
		})
	}
}
