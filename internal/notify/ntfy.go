package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/config"
	"github.com/belcaik/open-nvr/internal/logx"
)

const (
	PriorityLow     = 2
	PriorityDefault = 3
	PriorityHigh    = 4
	PriorityUrgent  = 5
)

// Payload se publica en JSON en la raíz del servidor (topic en el cuerpo),
// así títulos y mensajes UTF-8 llegan sin codificar cabeceras.
type Payload struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
}

func BuildPayload(topic, title, message string, priority int, tags []string) Payload {
	if tags == nil {
		tags = []string{}
	}
	return Payload{Topic: topic, Title: config.Redact(title), Message: config.Redact(message),
		Priority: priority, Tags: tags}
}

type Sender func(url string, p Payload, token string) error

var httpClient = &http.Client{Timeout: 10 * time.Second}

func HTTPSender(url string, p Payload, token string) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(url, "/")+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy respondió HTTP %d", resp.StatusCode)
	}
	return nil
}

// Notifier envía avisos con deduplicación. Sin URL funciona en modo
// prueba: solo escribe el aviso en el registro.
type Notifier struct {
	URL, Topic, Token, Prefix string
	Throttle                  *Throttle
	Send                      Sender // nil = HTTPSender
}

// Notify devuelve true si el aviso se envió (o se registró en modo prueba).
// interval < 0 usa el intervalo por defecto del Throttle.
func (n *Notifier) Notify(key, title, message string, priority int, tags []string, interval, now float64) bool {
	if !n.Throttle.Allow(key, now, interval) {
		logx.Printf("aviso suprimido (dedup/límite): %s", title)
		return false
	}
	suppressed := n.Throttle.TakeSuppressed()
	if suppressed > 0 {
		message += fmt.Sprintf("\n\n(%d avisos anteriores suprimidos por dedup/límite)", suppressed)
	}
	p := BuildPayload(n.Topic, n.Prefix+title, message, priority, tags)
	if n.URL == "" {
		logx.Printf("[ntfy desactivado] %s: %s", p.Title, p.Message)
		return true
	}
	send := n.Send
	if send == nil {
		send = HTTPSender
	}
	if err := send(n.URL, p, n.Token); err != nil {
		// Red o ntfy caídos: no debe parar el bucle y el aviso se reintenta.
		n.Throttle.Forget(key, now)
		n.Throttle.State.Suppressed += suppressed
		logx.Printf("error enviando a ntfy: %T: %v", err, err)
		return false
	}
	logx.Printf("aviso enviado: %s", p.Title)
	return true
}
