package nvr

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSetCameraPassword(t *testing.T) {
	f, c := newFakeNVR(t)
	if err := c.SetCameraPassword(context.Background(), 2, "Clave2026segura"); err != nil {
		t.Fatal(err)
	}
	body := f.last(t, "PUT", "/System/FrontConnectDevicetList").body
	for _, want := range []string{"<ChannelID>2</ChannelID>", "<Password>Clave2026segura</Password>", "<ActionState>modify</ActionState>", "<DID>XXXXXX-000000-XXXXX</DID>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en PUT: %s", want, body)
		}
	}
	if strings.Contains(body, "<ChannelID>1</ChannelID>") || strings.Count(body, "<DevConnect Version=") != 1 {
		t.Fatalf("PUT contiene otra cámara: %s", body)
	}
}

func TestSetCameraPasswordValidationDoesNotWrite(t *testing.T) {
	f, c := newFakeNVR(t)
	for _, password := range []string{"", "corta1", "solo letras largas", "12345678", "sinDigitos", "12345678901234567"} {
		if err := c.SetCameraPassword(context.Background(), 1, password); err == nil {
			t.Fatalf("contraseña %q aceptada", password)
		}
	}
	if got := f.count(); got != 0 {
		t.Fatalf("hubo %d peticiones con entradas inválidas", got)
	}
}

func TestSetCameraPasswordRejectsSamePassword(t *testing.T) {
	f, c := newFakeNVR(t)
	f.raw["GET /System/FrontConnectDevicetList"] = []byte(`<DevConnectList><DevConnect><ChannelID>1</ChannelID><Password>Clave2026segura</Password><ActionState /></DevConnect></DevConnectList>`)
	if err := c.SetCameraPassword(context.Background(), 1, "Clave2026segura"); err == nil {
		t.Fatal("aceptó la misma contraseña")
	}
	if got := f.count(); got != 1 {
		t.Fatalf("peticiones = %d, quiero sólo GET", got)
	}
}

func TestSetCameraPasswordRestoresPreviousCredential(t *testing.T) {
	f, c := newFakeNVR(t)
	f.raw["GET /System/FrontConnectDevicetList"] = []byte(`<DevConnectList><DevConnect><ChannelID>1</ChannelID><Password>Anterior2026</Password><ActionState /></DevConnect></DevConnectList>`)
	f.raw["GET /System/FrontConnectDeviceStatusList"] = []byte(`<DevStatusList><DevStatus><LoginStatus>login failed</LoginStatus></DevStatus></DevStatusList>`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.SetCameraPassword(ctx, 1, "Nueva2026")
	if err == nil || !strings.Contains(err.Error(), "restauró la credencial anterior") {
		t.Fatalf("error = %v", err)
	}
	body := f.last(t, "PUT", "/System/FrontConnectDevicetList").body
	if !strings.Contains(body, "<Password>Anterior2026</Password>") {
		t.Fatalf("último PUT no restauró: %s", body)
	}
}
