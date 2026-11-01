package nvr

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"
)

func fastCameraPasswordTiming(t *testing.T) {
	t.Helper()
	timeout, poll := cameraPasswordTimeout, cameraPasswordPoll
	cameraPasswordTimeout, cameraPasswordPoll = 150*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { cameraPasswordTimeout, cameraPasswordPoll = timeout, poll })
}

func devConnectList(channel int, password string) []byte {
	return []byte(`<DevConnectList><DevConnect><ChannelID>` + itoa(channel) + `</ChannelID><DID>XXXXXX-000000-XXXXX</DID><Password>` + password + `</Password><ActionState /></DevConnect><DevConnect><ChannelID>9</ChannelID><Password>Otra2026</Password><ActionState /></DevConnect></DevConnectList>`)
}

func itoa(n int) string { return string(rune('0' + n)) }

func devStatusList(channel int, status string) []byte {
	var b strings.Builder
	b.WriteString(`<DevStatusList>`)
	for i := 1; i <= 4; i++ {
		s := "weak password"
		if i == channel {
			s = status
		}
		b.WriteString(`<DevStatus><LoginStatus>` + s + `</LoginStatus></DevStatus>`)
	}
	b.WriteString(`</DevStatusList>`)
	return []byte(b.String())
}

// cameraScript simula la IPC: before es el estado previo y cada PUT avanza
// a la siguiente lista de estados, que se consumen en orden y repiten el último.
type cameraScript struct {
	channel  int
	list     []byte
	before   string
	afterPut [][]string
	putReply []byte
	puts     int
	polls    int
}

func (s *cameraScript) install(f *fakeNVR) {
	f.hook = func(key string) ([]byte, bool) {
		switch key {
		case "GET /System/FrontConnectDevicetList":
			return s.list, true
		case "PUT /System/FrontConnectDevicetList":
			s.puts++
			s.polls = 0
			if s.putReply != nil {
				return s.putReply, true
			}
		case "GET /System/FrontConnectDeviceStatusList":
			if s.puts == 0 {
				return devStatusList(s.channel, s.before), true
			}
			states := s.afterPut[min(s.puts, len(s.afterPut))-1]
			state := states[min(s.polls, len(states)-1)]
			s.polls++
			return devStatusList(s.channel, state), true
		}
		return nil, false
	}
}

func assertNoSecrets(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatal("el error contiene una contraseña")
		}
	}
}

func TestSetCameraPassword(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 2, list: devConnectList(2, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"weak password", "offline", "connect success"}}}
	script.install(f)
	if err := c.SetCameraPassword(context.Background(), 2, "Clave2026segura"); err != nil {
		t.Fatal(err)
	}
	body := f.last(t, "PUT", "/System/FrontConnectDevicetList").body
	for _, want := range []string{"<ChannelID>2</ChannelID>", "<Password>Clave2026segura</Password>", "<ActionState>modify</ActionState>", "<DID>XXXXXX-000000-XXXXX</DID>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en PUT", want)
		}
	}
	if strings.Contains(body, "<ChannelID>9</ChannelID>") || strings.Count(body, "<DevConnect>") != 1 {
		t.Fatal("PUT contiene otra cámara")
	}
	if script.puts != 1 {
		t.Fatalf("PUT = %d, quiero 1", script.puts)
	}
}

func TestSetCameraPasswordAcceptsChangedOnlineStatus(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"connect success"}}}
	script.install(f)
	if err := c.SetCameraPassword(context.Background(), 1, "Clave2026segura"); err != nil {
		t.Fatal(err)
	}
	if script.puts != 1 {
		t.Fatalf("PUT = %d, quiero 1", script.puts)
	}
}

// El LoginStatus anterior al cambio no demuestra que la IPC reconectara con
// la clave nueva: sin transición no se confirma y se restaura la anterior.
func TestSetCameraPasswordIgnoresStaleOnlineStatus(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"weak password"}, {"weak password"}}}
	script.install(f)
	err := c.SetCameraPassword(context.Background(), 1, "Clave2026segura")
	if err == nil {
		t.Fatal("aceptó el estado previo como confirmación")
	}
	assertNoSecrets(t, err, "Anterior2026", "Clave2026segura")
	if script.puts != 2 || !strings.Contains(f.last(t, "PUT", "/System/FrontConnectDevicetList").body, "<Password>Anterior2026</Password>") {
		t.Fatalf("no restauró la credencial anterior (PUT = %d)", script.puts)
	}
}

func TestSetCameraPasswordRestoresPreviousCredential(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"login failed"}, {"login failed", "weak password"}}}
	script.install(f)
	err := c.SetCameraPassword(context.Background(), 1, "Nueva2026")
	var pwErr *CameraPasswordError
	if !errors.As(err, &pwErr) || !pwErr.Restored || !strings.Contains(err.Error(), "se restauró la credencial anterior") {
		t.Fatalf("error = %v", err)
	}
	assertNoSecrets(t, err, "Anterior2026", "Nueva2026")
	if !strings.Contains(f.last(t, "PUT", "/System/FrontConnectDevicetList").body, "<Password>Anterior2026</Password>") {
		t.Fatal("último PUT no restauró")
	}
}

func TestSetCameraPasswordReportsUnconfirmedRollback(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"login failed"}}}
	script.install(f)
	err := c.SetCameraPassword(context.Background(), 1, "Nueva2026")
	var pwErr *CameraPasswordError
	if !errors.As(err, &pwErr) || pwErr.Restored || strings.Contains(err.Error(), "se restauró la credencial anterior") {
		t.Fatalf("error = %v", err)
	}
	assertNoSecrets(t, err, "Anterior2026", "Nueva2026")
	if script.puts != 2 {
		t.Fatalf("PUT = %d, quiero 2", script.puts)
	}
}

func TestSetCameraPasswordSkipsRollbackWhenNVRRejects(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		putReply: []byte(`<ResponseStatus><statusCode>-1</statusCode></ResponseStatus>`)}
	script.install(f)
	if err := c.SetCameraPassword(context.Background(), 1, "Nueva2026"); err == nil {
		t.Fatal("aceptó un PUT rechazado")
	}
	if script.puts != 1 {
		t.Fatalf("PUT = %d: un rechazo explícito no requiere restaurar", script.puts)
	}
}

func TestSetCameraPasswordPreconditionsDoNotWrite(t *testing.T) {
	fastCameraPasswordTiming(t)
	cases := []struct {
		name   string
		list   []byte
		before string
	}{
		{"cámara desconectada", devConnectList(1, "Anterior2026"), "login failed"},
		{"credencial actual no disponible", devConnectList(1, ""), "weak password"},
		{"canal no registrado", devConnectList(3, "Anterior2026"), "weak password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFakeNVR(t)
			script := &cameraScript{channel: 1, list: tc.list, before: tc.before}
			script.install(f)
			if err := c.SetCameraPassword(context.Background(), 1, "Nueva2026"); err == nil {
				t.Fatal("debía rechazarse")
			}
			if script.puts != 0 {
				t.Fatalf("PUT = %d, quiero 0", script.puts)
			}
		})
	}
}

func TestSetCameraPasswordEscapesXML(t *testing.T) {
	fastCameraPasswordTiming(t)
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Anterior2026"), before: "weak password",
		afterPut: [][]string{{"connect success"}}}
	script.install(f)
	password := `A1&<>"'b`
	if err := c.SetCameraPassword(context.Background(), 1, password); err != nil {
		t.Fatal(err)
	}
	var got struct {
		DevConnect []struct {
			Password string `xml:"Password"`
		} `xml:"DevConnect"`
	}
	if err := xml.Unmarshal([]byte(f.last(t, "PUT", "/System/FrontConnectDevicetList").body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.DevConnect) != 1 || got.DevConnect[0].Password != password {
		t.Fatal("la contraseña no sobrevive al XML")
	}
}

func TestSetCameraPasswordValidationDoesNotWrite(t *testing.T) {
	f, c := newFakeNVR(t)
	for _, password := range []string{"", "corta1", "solo letras largas", "12345678", "sinDigitos", "12345678901234567", "Clave 2026", "Clave\t2026", "Contraseña2026"} {
		if err := c.SetCameraPassword(context.Background(), 1, password); err == nil {
			t.Fatalf("contraseña %q aceptada", password)
		}
	}
	for _, password := range []string{"Abcdef12", "Abcdefgh12345678"} {
		if err := validateCameraPassword(password); err != nil {
			t.Fatalf("límite válido %q rechazado: %v", password, err)
		}
	}
	if got := f.count(); got != 0 {
		t.Fatalf("hubo %d peticiones con entradas inválidas", got)
	}
}

func TestSetCameraPasswordRejectsSamePassword(t *testing.T) {
	f, c := newFakeNVR(t)
	script := &cameraScript{channel: 1, list: devConnectList(1, "Clave2026segura"), before: "weak password"}
	script.install(f)
	if err := c.SetCameraPassword(context.Background(), 1, "Clave2026segura"); err == nil {
		t.Fatal("aceptó la misma contraseña")
	}
	if script.puts != 0 {
		t.Fatalf("PUT = %d, quiero 0", script.puts)
	}
}
