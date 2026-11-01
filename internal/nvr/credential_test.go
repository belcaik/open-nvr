package nvr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func fastCredentialTiming(t *testing.T) {
	t.Helper()
	timeout, poll := cameraCredentialTimeout, cameraCredentialPoll
	cameraCredentialTimeout, cameraCredentialPoll = 300*time.Millisecond, time.Millisecond
	t.Cleanup(func() { cameraCredentialTimeout, cameraCredentialPoll = timeout, poll })
}

func devConnectList(channel int, user, password string) []byte {
	return []byte(`<DevConnectList><DevConnect><ChannelID>` + itoa(channel) + `</ChannelID><Username>` + user + `</Username><Password>` + password + `</Password><DID>XXXXXX-000000-XXXXX</DID><ActionState /></DevConnect><DevConnect><ChannelID>9</ChannelID><Username>admin</Username><Password>Otra2026</Password><ActionState /></DevConnect></DevConnectList>`)
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

// credentialScript simula el NVR: before es el estado antes del PUT y after
// la secuencia de estados que se lee después.
type credentialScript struct {
	list   []byte
	before string
	after  []string
	puts   int
	polls  int
}

func (s *credentialScript) install(f *fakeNVR, channel int) {
	f.hook = func(key string) ([]byte, bool) {
		switch key {
		case "GET /System/FrontConnectDevicetList":
			return s.list, true
		case "PUT /System/FrontConnectDevicetList":
			s.puts++
		case "GET /System/FrontConnectDeviceStatusList":
			if s.puts == 0 {
				return devStatusList(channel, s.before), true
			}
			state := s.after[min(s.polls, len(s.after)-1)]
			s.polls++
			return devStatusList(channel, state), true
		}
		return nil, false
	}
}

func TestSetCameraCredentialReconnects(t *testing.T) {
	fastCredentialTiming(t)
	f, c := newFakeNVR(t)
	script := &credentialScript{list: devConnectList(2, "admin", ""), before: "login failed", after: []string{"connectting", "connect success"}}
	script.install(f, 2)
	if err := c.SetCameraCredential(context.Background(), 2, "operador", "Clave&2026"); err != nil {
		t.Fatal(err)
	}
	body := f.last(t, "PUT", "/System/FrontConnectDevicetList").body
	for _, want := range []string{"<ChannelID>2</ChannelID>", "<Username>operador</Username>", "<Password>Clave&amp;2026</Password>", "<ActionState>modify</ActionState>", "<DID>XXXXXX-000000-XXXXX</DID>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en %s", want, body)
		}
	}
	if strings.Contains(body, "<ChannelID>9</ChannelID>") {
		t.Fatal("el PUT incluye otra cámara")
	}
}

// Si la cámara ya estaba conectada, un estado online leído justo después del
// PUT puede ser el anterior: hace falta ver una transición.
func TestSetCameraCredentialIgnoresStaleOnlineStatus(t *testing.T) {
	fastCredentialTiming(t)
	f, c := newFakeNVR(t)
	script := &credentialScript{list: devConnectList(1, "admin", ""), before: "weak password", after: []string{"weak password", "login failed"}}
	script.install(f, 1)
	err := c.SetCameraCredential(context.Background(), 1, "admin", "Clave2026")
	var credErr *CameraCredentialError
	if !errors.As(err, &credErr) || credErr.Status != "login failed" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "Clave2026") {
		t.Fatal("el error contiene la contraseña")
	}
}

func TestSetCameraCredentialAcceptsTransitionFromOnline(t *testing.T) {
	fastCredentialTiming(t)
	f, c := newFakeNVR(t)
	script := &credentialScript{list: devConnectList(1, "admin", ""), before: "weak password", after: []string{"weak password", "connectting", "connect success"}}
	script.install(f, 1)
	if err := c.SetCameraCredential(context.Background(), 1, "admin", "Clave2026"); err != nil {
		t.Fatal(err)
	}
}

func TestSetCameraCredentialValidationDoesNotWrite(t *testing.T) {
	cases := []struct {
		channel        int
		user, password string
	}{
		{0, "admin", "Clave2026"},
		{1, "", "Clave2026"},
		{1, "admin", ""},
		{1, "admin", "con espacio"},
		{1, "admin", strings.Repeat("x", 33)},
		{1, "ádmin", "Clave2026"},
	}
	for _, tc := range cases {
		f, c := newFakeNVR(t)
		script := &credentialScript{list: devConnectList(1, "admin", ""), before: "login failed", after: []string{"connect success"}}
		script.install(f, 1)
		var invalid *ValidationError
		if err := c.SetCameraCredential(context.Background(), tc.channel, tc.user, tc.password); !errors.As(err, &invalid) {
			t.Fatalf("%+v: error = %v", tc, err)
		}
		if script.puts != 0 {
			t.Fatalf("%+v: escribió", tc)
		}
	}
}

func TestSetCameraCredentialUnknownChannelDoesNotWrite(t *testing.T) {
	f, c := newFakeNVR(t)
	script := &credentialScript{list: devConnectList(3, "admin", ""), before: "login failed", after: []string{"connect success"}}
	script.install(f, 1)
	if err := c.SetCameraCredential(context.Background(), 1, "admin", "Clave2026"); err == nil || script.puts != 0 {
		t.Fatalf("err=%v puts=%d", err, script.puts)
	}
}
