package nvr

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsAuthMsg es el primer mensaje que el cliente envía al WebSocket del NVR,
// según docs/api-reference.md §3 (common.js:1622-1655).
type wsAuthMsg struct {
	Auth struct {
		User string `json:"user"`
		Mode string `json:"mode"`
		Time string `json:"time"`
		Pass string `json:"pass"`
	} `json:"auth"`
	Req struct {
		Fun    string `json:"fun"`
		Ctrl   string `json:"ctrl"`
		Ch     string `json:"ch"`
		Stream string `json:"stream"`
	} `json:"req"`
}

// fakeWSEvents es el WebSocket del NVR falso: valida el mensaje de
// autenticación, responde con el reply y envía los eventos programados.
// Registra cuántas conexiones recibe y los mensajes de autenticación.
type fakeWSEvents struct {
	user, pass string
	ch         string   // canal que debe pedir el cliente
	replyCode  int      // code del reply de autenticación
	events     []string // mensajes a enviar tras el reply, en cada conexión
	closeAfter bool     // cerrar tras enviar los eventos (fuerza reconexión)

	srv     *httptest.Server
	conns   atomic.Int32
	auths   chan wsAuthMsg
	badAuth atomic.Int32 // autenticaciones que no superaron la validación
}

func newFakeWSEvents(t *testing.T, ch string) (*fakeWSEvents, *Client) {
	t.Helper()
	f := &fakeWSEvents{
		user:  "admin",
		pass:  "clave",
		ch:    ch,
		auths: make(chan wsAuthMsg, 16),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f, New(f.srv.URL, f.user, f.pass)
}

// validAuth comprueba el mensaje de autenticación igual que se espera que lo
// haga el NVR: md5(pass+time) en hex y la petición fun=event del canal dado.
func (f *fakeWSEvents) validAuth(a wsAuthMsg) bool {
	sum := md5.Sum([]byte(f.pass + a.Auth.Time))
	return a.Auth.User == f.user &&
		a.Auth.Mode == "0" &&
		a.Auth.Time != "" &&
		a.Auth.Pass == hex.EncodeToString(sum[:]) &&
		a.Req.Fun == "event" &&
		a.Req.Ctrl == "open" &&
		a.Req.Ch == f.ch &&
		a.Req.Stream == "1"
}

func (f *fakeWSEvents) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/WebSocket/Connect" {
		http.NotFound(w, r)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	f.conns.Add(1)
	defer conn.Close(websocket.StatusNormalClosure, "fin")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return
	}
	var a wsAuthMsg
	if err := json.Unmarshal(data, &a); err != nil {
		conn.Close(websocket.StatusProtocolError, "json inválido")
		return
	}
	f.auths <- a
	code := f.replyCode
	if !f.validAuth(a) {
		f.badAuth.Add(1)
		code = 1
	}
	reply := fmt.Sprintf(`{"cmd":"reply","code":%d,"msg":"success"}`, code)
	if conn.Write(ctx, websocket.MessageText, []byte(reply)) != nil {
		return
	}
	if code != 0 {
		return
	}
	for _, ev := range f.events {
		if conn.Write(ctx, websocket.MessageText, []byte(ev)) != nil {
			return
		}
	}
	if f.closeAfter {
		return // el defer cierra y el cliente debe reconectar
	}
	conn.Read(ctx) // queda abierta hasta que el cliente cierre
}

// waitEvent recibe un evento con timeout para que un fallo no cuelgue el test.
func waitEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("el canal de eventos se cerró antes de tiempo")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando un evento")
		return Event{}
	}
}

func TestEventsAuthAndReceive(t *testing.T) {
	f, c := newFakeWSEvents(t, "1")
	// HIPÓTESIS: los mensajes de evento reales todavía no se han observado
	// (la detección de movimiento estaba desactivada en la captura). Estos
	// ejemplos siguen el handler JS de docs/api-reference.md §3: cmd:"event"
	// con body.type, body.param2 (canal visual) y body.param3 (4 = fin).
	f.events = []string{
		`{"cmd":"event","body":{"type":"motion","param2":"1","param3":1}}`,
		`{"cmd":"reply","code":0,"msg":"heartbeat"}`,
		`{"cmd":"event","body":{"type":"motion","param2":"1","param3":4}}`,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := c.Events(ctx, []int{1})

	// El servidor debe recibir un mensaje de autenticación válido.
	select {
	case a := <-f.auths:
		if !f.validAuth(a) {
			t.Fatalf("autenticación inválida: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando el mensaje de autenticación")
	}

	ev1 := waitEvent(t, events)
	if ev1.Kind != "motion" || ev1.Channel != 1 || !ev1.Active {
		t.Fatalf("evento 1 = %+v, quiero motion/1/activo", ev1)
	}
	if len(ev1.Raw) == 0 {
		t.Fatal("evento 1 sin Raw")
	}
	if d := time.Since(ev1.Time); d < 0 || d > 10*time.Second {
		t.Fatalf("evento 1 con Time extraña: %v", ev1.Time)
	}
	// El reply intermedio NO se emite: el siguiente evento es el fin.
	ev2 := waitEvent(t, events)
	if ev2.Kind != "motion" || ev2.Channel != 1 || ev2.Active {
		t.Fatalf("evento 2 = %+v, quiero motion/1/fin", ev2)
	}

	cancel()
	// Al cancelar, los dos canales se cierran sin más eventos ni errores.
	for ev := range events {
		t.Errorf("evento de más tras cancelar: %+v", ev)
	}
	for err := range errs {
		t.Errorf("error inesperado: %v", err)
	}
}

func TestEventsReconnect(t *testing.T) {
	// Esperas de reconexión acortadas para que el test sea rápido.
	oldBase, oldMax := reconnectBase, reconnectMax
	reconnectBase, reconnectMax = 10*time.Millisecond, 50*time.Millisecond
	defer func() { reconnectBase, reconnectMax = oldBase, oldMax }()

	f, c := newFakeWSEvents(t, "2")
	f.closeAfter = true // cada conexión envía un evento y se cae
	f.events = []string{`{"type":"motion","ch":2,"status":"start"}`}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := c.Events(ctx, []int{2})

	waitEvent(t, events)
	waitEvent(t, events) // solo puede llegar por una segunda conexión
	if n := f.conns.Load(); n < 2 {
		t.Fatalf("conexiones = %d, quiero al menos 2 (reconexión)", n)
	}
	cancel()
	for range events {
	}
	for err := range errs {
		t.Errorf("error inesperado: %v", err)
	}
}

func TestEventsUnauthorized(t *testing.T) {
	f, c := newFakeWSEvents(t, "1")
	f.replyCode = 1 // el NVR rechaza la autenticación
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs := c.Events(ctx, []int{1})

	select {
	case err, ok := <-errs:
		if !ok {
			t.Fatal("errc cerrado sin error")
		}
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, quiero ErrUnauthorized", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando ErrUnauthorized")
	}
	// El canal se detiene sin reconectar y los canales se cierran.
	for range events {
	}
	for range errs {
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.conns.Load(); n != 1 {
		t.Fatalf("conexiones = %d, quiero 1 (sin reconexión tras un 401 lógico)", n)
	}
}

func TestEventsContextCancel(t *testing.T) {
	f, c := newFakeWSEvents(t, "3")
	ctx, cancel := context.WithCancel(context.Background())
	events, errs := c.Events(ctx, []int{3})
	// Espera a que la conexión esté establecida antes de cancelar.
	select {
	case <-f.auths:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout esperando la conexión")
	}
	cancel()
	for range events {
	}
	for range errs {
	}
}

func TestParseEventHeuristic(t *testing.T) {
	// HIPÓTESIS: ninguno de estos mensajes se ha observado en el equipo real;
	// son ejemplos inventados a partir del handler JS documentado en
	// docs/api-reference.md §3 y de claves plausibles del firmware. Sirven
	// para fijar la heurística tolerante del parser hasta la Tarea 5.
	cases := []struct {
		name   string
		msg    string
		kind   string
		ch     int
		active bool
	}{
		{"formato documentado inicio", `{"cmd":"event","body":{"type":"motion","param2":"1","param3":1}}`, "motion", 1, true},
		{"formato documentado fin", `{"cmd":"event","body":{"type":"motion","param2":"1","param3":4}}`, "motion", 1, false},
		{"plano con status start", `{"type":"videolost","ch":2,"status":"start"}`, "videolost", 2, true},
		{"plano con state stop", `{"event":"ai","channel":"3","state":"stop"}`, "ai", 3, false},
		{"anidado con mayúsculas", `{"Alarm":{"Type":"VideoLost","Channel":4,"Status":"off"}}`, "videolost", 4, false},
		{"tipo desconocido se conserva crudo", `{"type":"videoShade","ch":1}`, "videoShade", 1, true},
		{"sin tipo", `{"foo":"bar"}`, "unknown", 0, true},
		{"json inválido", `no es json`, "unknown", 0, true},
		{"binario o vacío", ``, "unknown", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := parseEvent(json.RawMessage(tc.msg))
			if ev.Kind != tc.kind || ev.Channel != tc.ch || ev.Active != tc.active {
				t.Fatalf("parseEvent(%s) = kind %q ch %d active %v, quiero %q/%d/%v",
					tc.msg, ev.Kind, ev.Channel, ev.Active, tc.kind, tc.ch, tc.active)
			}
			if string(ev.Raw) != tc.msg {
				t.Fatalf("Raw = %q, quiero el mensaje completo", ev.Raw)
			}
		})
	}
}
