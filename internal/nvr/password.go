package nvr

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	cameraPasswordTimeout = 8 * time.Second
	cameraPasswordPoll    = 200 * time.Millisecond
)

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

func validateCameraPassword(password string) error {
	if len(password) < 8 || len(password) > 16 {
		return &ValidationError{Field: "Password", Reason: "debe tener entre 8 y 16 caracteres"}
	}
	letters, digits := 0, 0
	for _, b := range []byte(password) {
		if b < 33 || b > 126 {
			return &ValidationError{Field: "Password", Reason: "debe usar caracteres ASCII visibles, sin espacios"}
		}
		switch {
		case b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z':
			letters++
		case b >= '0' && b <= '9':
			digits++
		}
	}
	if letters == 0 || digits == 0 {
		return &ValidationError{Field: "Password", Reason: "debe incluir al menos una letra y un número"}
	}
	return nil
}

func cameraPasswordBodies(doc []byte, channel int, password string) (updated, rollback []byte, err error) {
	var root xmlNode
	if err := xml.Unmarshal(doc, &root); err != nil {
		return nil, nil, &BadResponseError{Path: "/System/FrontConnectDevicetList", HTTP: http.StatusOK}
	}
	var selected *xmlNode
	for i := range root.Children {
		entry := &root.Children[i]
		if entry.XMLName.Local != "DevConnect" {
			continue
		}
		id := entry.child("ChannelID")
		if id != nil && id.Text == strconv.Itoa(channel) {
			selected = entry
			break
		}
	}
	if selected == nil || selected.child("Password") == nil {
		return nil, nil, &ValidationError{Field: "ChannelID", Reason: "la cámara no está registrada"}
	}
	previous := selected.child("Password").Text
	if previous == password {
		return nil, nil, &ValidationError{Field: "Password", Reason: "debe ser distinta de la actual"}
	}
	action := selected.child("ActionState")
	if action == nil {
		selected.Children = append(selected.Children, xmlNode{XMLName: xml.Name{Local: "ActionState"}})
		action = &selected.Children[len(selected.Children)-1]
	}
	action.Text = "modify"
	selected.child("Password").Text = password
	updated, err = marshalDevConnect(root, *selected)
	if err != nil {
		return nil, nil, err
	}
	selected.child("Password").Text = previous
	rollback, err = marshalDevConnect(root, *selected)
	return updated, rollback, err
}

func marshalDevConnect(root, selected xmlNode) ([]byte, error) {
	root.Text = ""
	selected.Text = ""
	root.Children = []xmlNode{selected}
	body, err := xml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("nvr: serializar credencial IPC: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}

func (c *Client) SetCameraPassword(ctx context.Context, channel int, password string) error {
	if channel < 1 {
		return &ValidationError{Field: "ChannelID", Reason: "debe ser positivo"}
	}
	if err := validateCameraPassword(password); err != nil {
		return err
	}
	c.cameraPasswordMu.Lock()
	defer c.cameraPasswordMu.Unlock()

	const path = "/System/FrontConnectDevicetList"
	doc, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return err
	}
	updated, rollback, err := cameraPasswordBodies(doc, channel, password)
	if err != nil {
		return err
	}
	if _, err = c.do(ctx, http.MethodPut, path, string(updated), controlTimeout); err != nil {
		return c.rollbackCameraPassword(ctx, path, rollback, err)
	}
	if err = c.waitCameraPassword(ctx, channel); err != nil {
		return c.rollbackCameraPassword(ctx, path, rollback, err)
	}
	return nil
}

func (c *Client) rollbackCameraPassword(ctx context.Context, path string, rollback []byte, cause error) error {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cameraPasswordTimeout)
	defer cancel()
	_, rollbackErr := c.do(rollbackCtx, http.MethodPut, path, string(rollback), controlTimeout)
	if rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("restaurar credencial anterior: %w", rollbackErr))
	}
	return errors.Join(cause, errors.New("la cámara no confirmó la nueva contraseña; se restauró la credencial anterior"))
}

func (c *Client) waitCameraPassword(ctx context.Context, channel int) error {
	waitCtx, cancel := context.WithTimeout(ctx, cameraPasswordTimeout)
	defer cancel()
	ticker := time.NewTicker(cameraPasswordPoll)
	defer ticker.Stop()
	for {
		status, err := c.cameraLoginStatus(waitCtx, channel)
		if err == nil && (status == "connect success" || status == "weak password") {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if err != nil {
				return err
			}
			return fmt.Errorf("nvr: estado IPC tras cambiar contraseña: %s", status)
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
