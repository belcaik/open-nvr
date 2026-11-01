package nvr

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// FrontConnectDevicetList con ActionState=modify sólo cambia la credencial con
// la que el NVR inicia sesión en la cámara, nunca la de la cámara (T104). Sirve
// para volver a conectar una cámara cuya clave se cambió directamente en ella.
// Un Password vacío se ignora, así que no permite volver a la clave de fábrica:
// para eso hay que eliminar el canal y dejar que el NVR la vuelva a añadir.

// Variables y no constantes para que los tests acorten la espera.
var (
	cameraCredentialTimeout = 20 * time.Second
	cameraCredentialPoll    = 500 * time.Millisecond
)

// CameraCredentialError: el NVR guardó la credencial pero la cámara no volvió
// a conectar. Status es el último LoginStatus leído; nunca lleva secretos.
type CameraCredentialError struct {
	Status string
}

func (e *CameraCredentialError) Error() string {
	return "nvr: la cámara no conectó con la credencial nueva (estado: " + e.Status + ")"
}

func cameraOnline(status string) bool {
	return status == "connect success" || status == "weak password"
}

type xmlNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []xmlNode  `xml:",any"`
}

func (n *xmlNode) child(name string) *xmlNode {
	for i := range n.Children {
		if n.Children[i].XMLName.Local == name {
			return &n.Children[i]
		}
	}
	return nil
}

func (n *xmlNode) set(name, value string) {
	if c := n.child(name); c != nil {
		c.Text = value
		return
	}
	n.Children = append(n.Children, xmlNode{XMLName: xml.Name{Local: name}, Text: value})
}

// validateCredentialField admite 1 a 32 caracteres ASCII visibles: la clave es
// la que el propietario puso en la cámara, así que no se impone complejidad.
func validateCredentialField(field, value string) error {
	if len(value) == 0 || len(value) > 32 {
		return &ValidationError{Field: field, Reason: "debe tener entre 1 y 32 caracteres"}
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return &ValidationError{Field: field, Reason: "debe usar caracteres ASCII visibles, sin espacios"}
		}
	}
	return nil
}

// SetCameraCredential guarda en el NVR el usuario y la clave de la cámara del
// canal y espera a que vuelva a conectar. No hay rollback posible: el NVR no
// devuelve ni acepta restaurar una clave vacía.
func (c *Client) SetCameraCredential(ctx context.Context, channel int, username, password string) error {
	if channel < 1 {
		return &ValidationError{Field: "ChannelID", Reason: "debe ser positivo"}
	}
	if err := validateCredentialField("Username", username); err != nil {
		return err
	}
	if err := validateCredentialField("Password", password); err != nil {
		return err
	}
	const path = "/System/FrontConnectDevicetList"
	doc, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	var root xmlNode
	if err := xml.Unmarshal(doc, &root); err != nil {
		return &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	var selected *xmlNode
	for i := range root.Children {
		entry := &root.Children[i]
		if id := entry.child("ChannelID"); entry.XMLName.Local == "DevConnect" && id != nil && id.Text == strconv.Itoa(channel) {
			selected = entry
			break
		}
	}
	if selected == nil {
		return &ValidationError{Field: "ChannelID", Reason: "no hay ninguna cámara registrada en ese canal"}
	}
	before, err := c.cameraLoginStatus(ctx, channel)
	if err != nil {
		return err
	}
	selected.set("Username", username)
	selected.set("Password", password)
	selected.set("ActionState", "modify")
	selected.Text = ""
	root.Text = ""
	root.Children = []xmlNode{*selected}
	body, err := xml.Marshal(root)
	if err != nil {
		return fmt.Errorf("nvr: serializar credencial IPC: %w", err)
	}
	if _, err := c.do(ctx, http.MethodPut, path, xml.Header+string(body), controlTimeout); err != nil {
		return err
	}
	return c.waitCameraOnline(ctx, channel, before)
}

// waitCameraOnline acepta un estado online sólo si es una transición: si la
// cámara ya estaba conectada, el primer estado leído puede ser el anterior.
func (c *Client) waitCameraOnline(ctx context.Context, channel int, before string) error {
	waitCtx, cancel := context.WithTimeout(ctx, cameraCredentialTimeout)
	defer cancel()
	ticker := time.NewTicker(cameraCredentialPoll)
	defer ticker.Stop()
	sawOffline := !cameraOnline(before)
	status := before
	for {
		if s, err := c.cameraLoginStatus(waitCtx, channel); err == nil {
			status = s
			if !cameraOnline(status) {
				sawOffline = true
			} else if sawOffline || status != before {
				return nil
			}
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &CameraCredentialError{Status: status}
		case <-ticker.C:
		}
	}
}

func (c *Client) cameraLoginStatus(ctx context.Context, channel int) (string, error) {
	const path = "/System/FrontConnectDeviceStatusList"
	body, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return "", err
	}
	var status devStatusListXML
	if err := xml.Unmarshal(body, &status); err != nil || channel > len(status.Status) {
		return "", &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	return status.Status[channel-1].LoginStatus, nil
}
