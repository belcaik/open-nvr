package nvr

import (
	"context"
	"testing"
)

func TestPTZBodyAndSpeedClamp(t *testing.T) {
	f, c := newFakeNVR(t)
	ctx := context.Background()
	// La velocidad 15 se limita a 10, el máximo del NVR.
	if err := c.PTZ(ctx, 2, TurnLeft, true, 15); err != nil {
		t.Fatal(err)
	}
	req := f.last(t, "PUT", "/PTZ/2/TurnLeft")
	if req.body != "Param1=1&Param2=10" {
		t.Fatalf("cuerpo = %q, quiero %q", req.body, "Param1=1&Param2=10")
	}
	if req.contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", req.contentType)
	}
	if err := c.PTZ(ctx, 2, TurnLeft, false, 15); err != nil {
		t.Fatal(err)
	}
	req = f.last(t, "PUT", "/PTZ/2/TurnLeft")
	if req.body != "Param1=0&Param2=10" {
		t.Fatalf("cuerpo = %q, quiero %q", req.body, "Param1=0&Param2=10")
	}
}

func TestValidPTZCommandRejectsUnknown(t *testing.T) {
	// Comandos que el NVR conoce pero la interfaz no ofrece, y rutas
	// construidas a mano: ninguno debe pasar.
	for _, s := range []string{"AutoLeftRight", "ApertureBigger", "../x", ""} {
		if _, ok := ValidPTZCommand(s); ok {
			t.Errorf("ValidPTZCommand(%q) = true", s)
		}
	}
	valid := []string{
		"TurnUp", "TurnDown", "TurnLeft", "TurnRight",
		"TurnUpLeft", "TurnUpRight", "TurnDownLeft", "TurnDownRight",
		"ZoomIn", "ZoomOut", "FocusNear", "FocusFar",
	}
	for _, s := range valid {
		if _, ok := ValidPTZCommand(s); !ok {
			t.Errorf("ValidPTZCommand(%q) = false", s)
		}
	}
}

func TestPresetRange(t *testing.T) {
	f, c := newFakeNVR(t)
	ctx := context.Background()
	// El índice es cero-based (0..255): fuera de rango falla sin llamar al NVR.
	for _, idx := range []int{-1, 256} {
		if err := c.Preset(ctx, 1, PresetGoto, idx); err == nil {
			t.Errorf("Preset(idx=%d) = nil, quiero error", idx)
		}
	}
	if n := f.count(); n != 0 {
		t.Fatalf("hubo %d peticiones HTTP con índices inválidos", n)
	}
	if err := c.Preset(ctx, 1, PresetGoto, 255); err != nil {
		t.Fatal(err)
	}
	req := f.last(t, "PUT", "/PTZ/1/Presets/Goto")
	if req.body != "Param1=255" {
		t.Fatalf("cuerpo = %q, quiero %q", req.body, "Param1=255")
	}
}
