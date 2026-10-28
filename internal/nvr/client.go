// Package nvr es el cliente tipado de la API HTTP del NVR 2NLF.
// Solo habla con el equipo por HTTP Basic y no registra nada: los errores
// se tipifican para que el llamador decida cómo informar.
package nvr

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrUnauthorized = errors.New("nvr: credenciales rechazadas")
	ErrUnavailable  = errors.New("nvr: no responde")
)

// BadResponseError: HTTP distinto de 200 o statusCode distinto de 0 en el
// XML ResponseStatus. El NVR también lo devuelve con HTTP 200 y una página
// HTML "Not Found" cuando la ruta no existe en su firmware.
type BadResponseError struct {
	Path   string
	HTTP   int
	Status string // statusCode del XML ResponseStatus, "" si no lo hay
}

func (e *BadResponseError) Error() string {
	if e.Status != "" {
		return fmt.Sprintf("nvr: %s: el NVR devolvió statusCode %s", e.Path, e.Status)
	}
	return fmt.Sprintf("nvr: %s: respuesta HTTP %d inesperada", e.Path, e.HTTP)
}

const (
	// controlTimeout vale para lecturas y comandos PTZ.
	controlTimeout = 5 * time.Second
	// snapshotTimeout es más largo porque el NVR tarda en capturar.
	snapshotTimeout = 15 * time.Second
)

// Client habla con un NVR. Como mucho hace 2 peticiones simultáneas, el
// mismo límite con el que trabaja bien el firmware (la UI original abusa y
// el equipo deja de responder).
type Client struct {
	base string
	user string
	pass string
	hc   *http.Client
	sem  chan struct{}
	loc  *time.Location
}

// New crea un cliente para base ("http://host:port") con auth Basic.
func New(base, user, pass string) *Client {
	return &Client{
		base: strings.TrimSuffix(base, "/"),
		user: user,
		pass: pass,
		hc:   &http.Client{},
		sem:  make(chan struct{}, 2),
		loc:  time.Local,
	}
}

// WithLocation configura la zona local usada por las horas del NVR.
func (c *Client) WithLocation(loc *time.Location) *Client {
	if loc == nil {
		loc = time.Local
	}
	c.loc = loc
	return c
}

// do ejecuta una petición respetando el semáforo de concurrencia y el
// timeout indicado, y traduce los errores del NVR a los tipos del paquete.
func (c *Client) do(ctx context.Context, method, path, body string, timeout time.Duration) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// Error de red o timeout: el NVR no responde.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &BadResponseError{Path: path, HTTP: resp.StatusCode}
	}
	if err := checkStatus(path, b); err != nil {
		return nil, err
	}
	return b, nil
}

// responseStatus es el XML de confirmación de las operaciones PUT del NVR.
type responseStatus struct {
	XMLName    xml.Name `xml:"ResponseStatus"`
	StatusCode string   `xml:"statusCode"`
}

// checkStatus detecta las respuestas de error que el NVR sirve con HTTP 200:
// un ResponseStatus con statusCode distinto de 0, o la página HTML
// "Not Found" de las rutas que el firmware no soporta.
func checkStatus(path string, b []byte) error {
	var rs responseStatus
	if err := xml.Unmarshal(b, &rs); err == nil && rs.XMLName.Local == "ResponseStatus" {
		if rs.StatusCode != "0" {
			return &BadResponseError{Path: path, HTTP: http.StatusOK, Status: rs.StatusCode}
		}
		return nil
	}
	head := bytes.ToLower(bytes.TrimSpace(b[:min(len(b), 64)]))
	if bytes.HasPrefix(head, []byte("<html")) && bytes.Contains(b, []byte("Not Found")) {
		return &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	return nil
}
