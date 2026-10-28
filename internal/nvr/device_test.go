package nvr

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceInfo(t *testing.T) {
	f, c := newFakeNVR(t)
	info, err := c.DeviceInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "N310B-11NEA-AI-Z02-P4" {
		t.Errorf("Model = %q", info.Model)
	}
	if info.VideoChannels != 10 {
		t.Errorf("VideoChannels = %d, quiero 10", info.VideoChannels)
	}
	if !info.TwoWayAudio {
		t.Error("TwoWayAudio = false")
	}
	// La petición lleva autenticación Basic.
	req := f.last(t, "GET", "/System/DeviceInfo")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:clave"))
	if req.auth != want {
		t.Errorf("Authorization = %q, quiero %q", req.auth, want)
	}
}

func TestCamerasJoinsStatus(t *testing.T) {
	_, c := newFakeNVR(t)
	cams, err := c.Cameras(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cams) != 4 {
		t.Fatalf("cámaras = %d, quiero 4", len(cams))
	}
	for i, cam := range cams {
		if cam.Channel != i+1 {
			t.Errorf("cams[%d].Channel = %d, quiero %d", i, cam.Channel, i+1)
		}
		if cam.LoginStatus != "weak password" {
			t.Errorf("cams[%d].LoginStatus = %q", i, cam.LoginStatus)
		}
		// "weak password" cuenta como en línea: es el estado real de las 4 cámaras.
		if !cam.Online {
			t.Errorf("cams[%d].Online = false", i)
		}
		if cam.Model != "W3CA-52PHM-S/W/BT" {
			t.Errorf("cams[%d].Model = %q", i, cam.Model)
		}
		if cam.Firmware != "1.8.1-20260112" {
			t.Errorf("cams[%d].Firmware = %q", i, cam.Firmware)
		}
		if cam.IP == "" {
			t.Errorf("cams[%d].IP vacía", i)
		}
	}
}

func TestCamerasSkipsOnlyEmptyChannels(t *testing.T) {
	list := `<DevConnectList><DevConnect><ChannelID>1</ChannelID><IP>0.0.0.0</IP><MAC>00:00:00:00:00:00</MAC></DevConnect>` +
		`<DevConnect><ChannelID>2</ChannelID><IP>192.168.0.20</IP><MAC /></DevConnect>` +
		`<DevConnect><ChannelID>3</ChannelID><IP /><MAC /></DevConnect></DevConnectList>`
	status := `<DevStatusList><DevStatus><LoginStatus /></DevStatus><DevStatus><LoginStatus>connect success</LoginStatus></DevStatus><DevStatus><LoginStatus /></DevStatus></DevStatusList>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/FrontConnectDevicetList" {
			w.Write([]byte(list))
		} else {
			w.Write([]byte(status))
		}
	}))
	defer srv.Close()
	cams, err := New(srv.URL, "u", "p").Cameras(context.Background())
	if err != nil || len(cams) != 1 || cams[0].Channel != 2 || !cams[0].Online {
		t.Fatalf("%v %+v", err, cams)
	}
}
