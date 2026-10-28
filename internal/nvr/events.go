package nvr

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// basicAuthHeader construye la cabecera Authorization Basic para el
// handshake WebSocket.
func basicAuthHeader(user, pass string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	return h
}

// Esperas de reconexión del WebSocket de eventos: 1 s duplicándose hasta un
// tope de 30 s. Son variables de paquete solo para que los tests puedan
// acortarlas.
var (
	reconnectBase = 1 * time.Second
	reconnectMax  = 30 * time.Second
)

// Event es un evento en vivo del NVR (movimiento, pérdida de vídeo, IA…)
// recibido por el WebSocket /WebSocket/Connect.
type Event struct {
	Time    time.Time       // hora de recepción
	Channel int             // si el mensaje lo indica; 0 si no
	Kind    string          // "motion", "videolost", "ai"… o el valor crudo del campo de tipo; "unknown" si no hay
	Active  bool            // inicio (true) o fin (false) si se puede deducir
	Raw     json.RawMessage // mensaje completo, siempre
}

// Events abre una conexión WebSocket por canal, autentica con
// md5(pass+epoch_ms) y emite eventos hasta que ctx termine. Reconecta con
// espera creciente (1 s → 30 s) si la conexión se cae. Los mensajes de
// respuesta ("cmd":"reply") no se emiten. Un code distinto de 0 en la
// respuesta de autenticación envía ErrUnauthorized por errc y detiene ese
// canal (sin reconexión). Los dos canales devueltos se cierran cuando todas
// las goroutines terminan.
func (c *Client) Events(ctx context.Context, channels []int) (<-chan Event, <-chan error) {
	out := make(chan Event)
	errc := make(chan error, len(channels))
	var wg sync.WaitGroup
	for _, ch := range channels {
		wg.Add(1)
		go func(ch int) {
			defer wg.Done()
			c.eventLoop(ctx, ch, out, errc)
		}(ch)
	}
	go func() {
		wg.Wait()
		close(out)
		close(errc)
	}()
	return out, errc
}

// eventLoop mantiene viva la suscripción de un canal: reconecta con espera
// creciente ante cualquier fallo salvo cancelación o rechazo de credenciales.
func (c *Client) eventLoop(ctx context.Context, ch int, out chan<- Event, errc chan<- error) {
	wait := reconnectBase
	for {
		if ctx.Err() != nil {
			return
		}
		authed, err := c.eventConn(ctx, ch, out)
		if ctx.Err() != nil {
			return // cancelado
		}
		if errors.Is(err, ErrUnauthorized) {
			// Credenciales rechazadas: se informa y no se reconecta.
			select {
			case errc <- err:
			case <-ctx.Done():
			}
			return
		}
		if authed {
			// La conexión llegó a autenticar: la espera vuelve al mínimo.
			wait = reconnectBase
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait *= 2
		if wait > reconnectMax {
			wait = reconnectMax
		}
	}
}

// eventConn hace una conexión completa: handshake, autenticación y lectura
// de eventos hasta que la conexión se cae o ctx termina. Devuelve authed=true
// si llegó a superar la respuesta de autenticación.
func (c *Client) eventConn(ctx context.Context, ch int, out chan<- Event) (authed bool, err error) {
	dctx, cancel := context.WithTimeout(ctx, controlTimeout)
	conn, _, err := websocket.Dial(dctx, c.eventsURL(), &websocket.DialOptions{
		// El Boa exige Basic en todas sus rutas HTTP; se envía también en el
		// handshake por si el upgrade lo exige (hipótesis no verificada).
		HTTPHeader: basicAuthHeader(c.user, c.pass),
	})
	cancel()
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "fin")
	conn.SetReadLimit(1 << 20) // mensajes pequeños; el límite por defecto es 32 KiB

	wctx, wcancel := context.WithTimeout(ctx, controlTimeout)
	err = conn.Write(wctx, websocket.MessageText, c.eventsAuth(ch))
	wcancel()
	if err != nil {
		return false, err
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return authed, err
		}
		// Las respuestas del NVR ("cmd":"reply") no son eventos; la primera
		// decide si la autenticación se aceptó.
		var head struct {
			Cmd  string `json:"cmd"`
			Code *int   `json:"code"`
		}
		if json.Unmarshal(data, &head) == nil && head.Cmd == "reply" {
			if !authed {
				if head.Code != nil && *head.Code != 0 {
					return false, fmt.Errorf("%w (code=%d)", ErrUnauthorized, *head.Code)
				}
				authed = true
			}
			continue
		}
		ev := parseEvent(data)
		ev.Time = time.Now()
		select {
		case out <- ev:
		case <-ctx.Done():
			return authed, ctx.Err()
		}
	}
}

// eventsURL convierte la base HTTP del cliente en la URL WebSocket del NVR.
func (c *Client) eventsURL() string {
	return "ws" + strings.TrimPrefix(c.base, "http") + "/WebSocket/Connect"
}

// eventsAuth construye el primer mensaje del protocolo
// (docs/api-reference.md §3, common.js:1622-1655): auth con
// pass = md5(pass + epoch_ms) en hex, y la petición fun=event del canal.
func (c *Client) eventsAuth(ch int) []byte {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	sum := md5.Sum([]byte(c.pass + now))
	msg := map[string]any{
		"auth": map[string]string{
			"user": c.user,
			"mode": "0",
			"time": now,
			"pass": hex.EncodeToString(sum[:]),
		},
		"req": map[string]string{
			"fun":    "event",
			"ctrl":   "open",
			"ch":     strconv.Itoa(ch),
			"stream": "1",
		},
	}
	b, _ := json.Marshal(msg)
	return b
}

// knownKinds normaliza los tipos de evento reconocidos a su nombre canónico
// en minúsculas; cualquier otro valor se conserva crudo.
var knownKinds = map[string]string{
	"motion":     "motion",
	"videolost":  "videolost",
	"video_lost": "videolost",
	"video-lost": "videolost",
	"ai":         "ai",
	"human":      "human",
	"vehicle":    "vehicle",
}

// parseEvent interpreta un mensaje de evento del NVR.
//
// Heurística (HIPÓTESIS, pendiente de verificar con el equipo en la Tarea 5,
// porque los eventos reales aún no se han observado): se recorre el JSON a
// cualquier nivel buscando claves sin distinguir mayúsculas:
//   - "type", "event", "alarm" con valor de texto → Kind. Los tipos
//     conocidos se normalizan a minúsculas; el resto se conserva crudo.
//   - "ch", "channel", "param2" (número o texto numérico) → Channel.
//     "param2" es el canal visual del handler JS documentado.
//   - "status", "state" → Active: start/on/open/begin/active/true/1 = inicio;
//     stop/off/close/end/inactive/false/0 = fin.
//   - "start" / "stop" con valor verdadero → Active true / false.
//   - "param3" == 4 → fin (el handler JS documentado trata param3=4 como
//     fin del evento); cualquier otro valor → inicio.
//
// Si no se puede deducir el estado, Active queda true: un evento que llega
// se interpreta como actividad. Los mensajes que no son JSON o no tienen
// campo de tipo devuelven Kind "unknown".
func parseEvent(raw json.RawMessage) Event {
	ev := Event{Kind: "unknown", Active: true, Raw: raw}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ev
	}
	walkJSON(v, func(key string, val any) {
		switch strings.ToLower(key) {
		case "type", "event", "alarm":
			if s, ok := val.(string); ok && s != "" {
				if k, known := knownKinds[strings.ToLower(s)]; known {
					ev.Kind = k
				} else {
					ev.Kind = s
				}
			}
		case "ch", "channel", "param2":
			if n, ok := jsonToInt(val); ok {
				ev.Channel = n
			}
		case "status", "state":
			if a, ok := jsonToActive(val); ok {
				ev.Active = a
			}
		case "start":
			if b, ok := jsonToBool(val); ok && b {
				ev.Active = true
			}
		case "stop":
			if b, ok := jsonToBool(val); ok && b {
				ev.Active = false
			}
		case "param3":
			if n, ok := jsonToInt(val); ok {
				ev.Active = n != 4
			}
		}
	})
	return ev
}

// walkJSON aplica fn a cada par clave/valor del JSON, recorriendo objetos y
// arrays anidados a cualquier profundidad.
func walkJSON(v any, fn func(key string, val any)) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			fn(k, val)
			walkJSON(val, fn)
		}
	case []any:
		for _, val := range t {
			walkJSON(val, fn)
		}
	}
}

// jsonToInt acepta números JSON y textos numéricos.
func jsonToInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

// jsonToBool acepta booleanos, números y textos habituales de verdad.
func jsonToBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "on", "yes":
			return true, true
		case "false", "0", "off", "no":
			return false, true
		}
	}
	return false, false
}

// jsonToActive interpreta valores de "status"/"state" como inicio o fin.
func jsonToActive(v any) (bool, bool) {
	if s, ok := v.(string); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "start", "on", "open", "begin", "active":
			return true, true
		case "stop", "off", "close", "end", "inactive":
			return false, true
		}
		return false, false
	}
	return jsonToBool(v)
}
