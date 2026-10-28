package nvr

import (
	"context"
	"encoding/xml"
	"net/http"
)

// DeviceInfo es la ficha del NVR (GET /System/DeviceInfo).
type DeviceInfo struct {
	Name, Model, Firmware string
	VideoChannels         int
	TwoWayAudio           bool
}

type deviceInfoXML struct {
	DeviceName         string `xml:"DeviceName"`
	DeviceTypeName     string `xml:"DeviceTypeName"`
	SoftWareVersion    string `xml:"SoftWareVersion"`
	VideoInputChannels int    `xml:"VideoInputChannels"`
	TwoWayAudio        string `xml:"TwoWayAudio"`
}

func (c *Client) DeviceInfo(ctx context.Context) (DeviceInfo, error) {
	const path = "/System/DeviceInfo"
	b, err := c.do(ctx, http.MethodGet, path, "", controlTimeout)
	if err != nil {
		return DeviceInfo{}, err
	}
	var x deviceInfoXML
	if err := xml.Unmarshal(b, &x); err != nil {
		return DeviceInfo{}, &BadResponseError{Path: path, HTTP: http.StatusOK}
	}
	return DeviceInfo{
		Name:          x.DeviceName,
		Model:         x.DeviceTypeName,
		Firmware:      x.SoftWareVersion,
		VideoChannels: x.VideoInputChannels,
		TwoWayAudio:   x.TwoWayAudio == "true",
	}, nil
}

// Camera es una cámara IPC conectada al NVR.
type Camera struct {
	Channel     int // 1..N
	IP          string
	Model       string // DevType
	Firmware    string // SoftwareVersion
	LoginStatus string // texto crudo de FrontConnectDeviceStatusList
	Online      bool   // LoginStatus es "connect success" o "weak password"
}

type devConnectListXML struct {
	Devs []struct {
		ChannelID       int    `xml:"ChannelID"`
		IP              string `xml:"IP"`
		MAC             string `xml:"MAC"`
		DevType         string `xml:"DevType"`
		SoftwareVersion string `xml:"SoftwareVersion"`
	} `xml:"DevConnect"`
}

type devStatusListXML struct {
	Status []struct {
		LoginStatus string `xml:"LoginStatus"`
	} `xml:"DevStatus"`
}

// Cameras une FrontConnectDevicetList y FrontConnectDeviceStatusList: la
// lista de estado va por índice (elemento i = canal i+1). Omite los canales
// sin IP ni MAC, igual que xmlparse.cameras del vigilante Python.
func (c *Client) Cameras(ctx context.Context) ([]Camera, error) {
	const listPath = "/System/FrontConnectDevicetList"
	lb, err := c.do(ctx, http.MethodGet, listPath, "", controlTimeout)
	if err != nil {
		return nil, err
	}
	var list devConnectListXML
	if err := xml.Unmarshal(lb, &list); err != nil {
		return nil, &BadResponseError{Path: listPath, HTTP: http.StatusOK}
	}
	const statusPath = "/System/FrontConnectDeviceStatusList"
	sb, err := c.do(ctx, http.MethodGet, statusPath, "", controlTimeout)
	if err != nil {
		return nil, err
	}
	var status devStatusListXML
	if err := xml.Unmarshal(sb, &status); err != nil {
		return nil, &BadResponseError{Path: statusPath, HTTP: http.StatusOK}
	}
	var cams []Camera
	for _, d := range list.Devs {
		// Canal vacío: sin IP ni MAC (como xmlparse.cameras del vigilante).
		if (d.IP == "" || d.IP == "0.0.0.0") && (d.MAC == "" || d.MAC == "00:00:00:00:00:00") {
			continue
		}
		cam := Camera{
			Channel:  d.ChannelID,
			IP:       d.IP,
			Model:    d.DevType,
			Firmware: d.SoftwareVersion,
		}
		if d.ChannelID >= 1 && d.ChannelID <= len(status.Status) {
			cam.LoginStatus = status.Status[d.ChannelID-1].LoginStatus
		}
		// Las cámaras reales informan "weak password" estando en línea.
		cam.Online = cam.LoginStatus == "connect success" || cam.LoginStatus == "weak password"
		cams = append(cams, cam)
	}
	return cams, nil
}
