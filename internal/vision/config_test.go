package vision

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validCameraConfig() CameraConfig {
	return CameraConfig{
		Enabled: true,
		Zones: []Zone{{
			ID: "door", Name: "Puerta", Pose: Pose{Preset: 2}, Alert: true,
			Points: []Point{{X: 0.1, Y: 0.1}, {X: 0.9, Y: 0.1}, {X: 0.5, Y: 0.9}},
		}},
		Tracking:   TrackingConfig{Enabled: true, Speed: 5, ZoomTargetHeight: 0.5, ReturnAfter: 10 * time.Second, MaxPanPulses: 20, MaxTiltPulses: 10, MaxZoomPulses: 10},
		HomePreset: 1,
	}
}

func TestStoreRoundTripOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := validCameraConfig()
	if err := store.Put(2, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Get(2)
	if !ok || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("Get = (%+v, %v), quiero %+v", got, ok, cfg)
	}
	all := loaded.All()
	if len(all) != 1 || !reflect.DeepEqual(all[2], cfg) {
		t.Fatalf("All = %+v", all)
	}
}

func TestStoreValidationRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CameraConfig)
	}{
		{"camera zero", func(*CameraConfig) {}},
		{"too few points", func(c *CameraConfig) { c.Zones[0].Points = c.Zones[0].Points[:2] }},
		{"too many points", func(c *CameraConfig) { c.Zones[0].Points = make([]Point, 33) }},
		{"point below zero", func(c *CameraConfig) { c.Zones[0].Points[0].X = -0.01 }},
		{"point above one", func(c *CameraConfig) { c.Zones[0].Points[0].Y = 1.01 }},
		{"empty zone id", func(c *CameraConfig) { c.Zones[0].ID = "" }},
		{"duplicate zone id", func(c *CameraConfig) { c.Zones = append(c.Zones, c.Zones[0]) }},
		{"speed below range", func(c *CameraConfig) { c.Tracking.Speed = 0 }},
		{"speed above range", func(c *CameraConfig) { c.Tracking.Speed = 11 }},
		{"zoom below range", func(c *CameraConfig) { c.Tracking.ZoomTargetHeight = 0.09 }},
		{"zoom above range", func(c *CameraConfig) { c.Tracking.ZoomTargetHeight = 0.91 }},
		{"return too soon", func(c *CameraConfig) { c.Tracking.ReturnAfter = 4 * time.Second }},
		{"negative home preset", func(c *CameraConfig) { c.HomePreset = -1 }},
		{"negative zone preset", func(c *CameraConfig) { c.Zones[0].Pose.Preset = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(filepath.Join(t.TempDir(), "vision.json"))
			if err != nil {
				t.Fatal(err)
			}
			cfg := validCameraConfig()
			test.mutate(&cfg)
			cam := 1
			if test.name == "camera zero" {
				cam = 0
			}
			if err := store.Put(cam, cfg); err == nil {
				t.Fatal("Put aceptó configuración inválida")
			}
		})
	}
}

func TestStoreCorruptFileIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision.json")
	original := []byte(`{"1":`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path); err == nil || !strings.Contains(err.Error(), "cargar") {
		t.Fatalf("NewStore error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("archivo cambió: %q", got)
	}
}
