package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/belcaik/open-nvr/internal/config"
	"github.com/belcaik/open-nvr/internal/logx"
)

func captureLog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	old := logx.Out
	logx.Out = &buf
	t.Cleanup(func() { logx.Out = old })
	return &buf
}

func TestPayloadIsRedacted(t *testing.T) {
	config.ResetSecrets()
	config.Register("p4ssw0rd")
	p := BuildPayload("t", "título p4ssw0rd", "cuerpo p4ssw0rd", 4, []string{"x"})
	if p.Topic != "t" || p.Priority != 4 || strings.Contains(p.Title+p.Message, "p4ssw0rd") {
		t.Fatalf("%+v", p)
	}
}

func TestSendUsesSenderAndReportsSuppressed(t *testing.T) {
	captureLog(t)
	type sent struct {
		url   string
		p     Payload
		token string
	}
	var got []sent
	n := &Notifier{URL: "https://ntfy.example", Topic: "topic", Token: "tok",
		Throttle: NewThrottle(&ThrottleState{}, 100, 10),
		Send: func(url string, p Payload, token string) error {
			got = append(got, sent{url, p, token})
			return nil
		}}
	if !n.Notify("k", "T", "m", PriorityDefault, nil, -1, 0) ||
		n.Notify("k", "T", "m", PriorityDefault, nil, -1, 10) ||
		!n.Notify("k", "T", "m2", PriorityDefault, nil, -1, 200) {
		t.Fatal("secuencia de envíos")
	}
	if len(got) != 2 || got[0].token != "tok" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(got[1].p.Message, "1 avisos anteriores suprimidos") {
		t.Fatal(got[1].p.Message)
	}
}

func TestFailedSendCanBeRetried(t *testing.T) {
	captureLog(t)
	calls := 0
	n := &Notifier{URL: "https://ntfy.example", Topic: "t",
		Throttle: NewThrottle(&ThrottleState{}, 100, 10),
		Send: func(string, Payload, string) error {
			calls++
			if calls == 1 {
				return errors.New("red caída")
			}
			return nil
		}}
	if n.Notify("k", "T", "m", PriorityDefault, nil, -1, 0) {
		t.Fatal("el primer envío falla")
	}
	if !n.Notify("k", "T", "m", PriorityDefault, nil, -1, 1) {
		t.Fatal("el reintento debía enviarse")
	}
}

func TestDryRunWithoutURL(t *testing.T) {
	buf := captureLog(t)
	n := &Notifier{Topic: "t", Throttle: NewThrottle(&ThrottleState{}, 100, 10),
		Send: func(string, Payload, string) error { t.Fatal("no debe enviar"); return nil }}
	if !n.Notify("k", "T", "m", PriorityDefault, nil, -1, 0) {
		t.Fatal("modo prueba devuelve true")
	}
	if !strings.Contains(buf.String(), "ntfy desactivado") {
		t.Fatal(buf.String())
	}
}

func TestLogNeverPrintsSecret(t *testing.T) {
	buf := captureLog(t)
	config.ResetSecrets()
	config.Register("p4ssw0rd")
	logx.Printf("fallo con %s en el mensaje", "p4ssw0rd")
	if strings.Contains(buf.String(), "p4ssw0rd") {
		t.Fatal(buf.String())
	}
}

func TestHTTPSenderPostsJSON(t *testing.T) {
	var gotPath, gotCT, gotAuth string
	var gotBody Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCT, gotAuth = r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
	}))
	defer srv.Close()
	p := Payload{Topic: "t", Title: "Tí", Message: "m", Priority: 3, Tags: []string{"x"}}
	if err := HTTPSender(srv.URL, p, "tok"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/" || gotCT != "application/json" || gotAuth != "Bearer tok" || gotBody.Title != "Tí" {
		t.Fatalf("%s %s %s %+v", gotPath, gotCT, gotAuth, gotBody)
	}
}

func TestHTTPSenderErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := HTTPSender(srv.URL, Payload{}, ""); err == nil {
		t.Fatal("401 debe ser error")
	}
}
