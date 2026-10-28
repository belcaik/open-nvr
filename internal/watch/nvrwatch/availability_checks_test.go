package nvrwatch

import (
	"reflect"
	"testing"
)

func TestAvailabilityDownAfterThresholdThenSingleUp(t *testing.T) {
	availability := NewAvailability(map[string]*AvailabilityState{}, 3)
	if availability.Update("tcp:80", false, 0, "timeout", 0) != nil || availability.Update("tcp:80", false, 60, "timeout", 0) != nil {
		t.Fatal("la caída se notificó antes del umbral")
	}
	down := availability.Update("tcp:80", false, 120, "timeout", 0)
	if down == nil || down.Kind != Down || down.Name != "tcp:80" {
		t.Fatal(down)
	}
	if availability.Update("tcp:80", false, 180, "", 0) != nil {
		t.Fatal("la caída se repitió")
	}
	up := availability.Update("tcp:80", true, 300, "abierto", 0)
	if up == nil || up.Kind != Up || up.Duration == nil || *up.Duration != 300 {
		t.Fatal(up)
	}
	if availability.Update("tcp:80", true, 360, "", 0) != nil {
		t.Fatal("la recuperación se repitió")
	}
}

func TestAvailabilityFlapBelowThresholdIsSilent(t *testing.T) {
	a := NewAvailability(map[string]*AvailabilityState{}, 2)
	if a.Update("rtsp", false, 0, "", 0) != nil || a.Update("rtsp", true, 60, "", 0) != nil || a.Update("rtsp", false, 120, "", 0) != nil {
		t.Fatal("un flap corto produjo un evento")
	}
}

func TestAvailabilityStateSurvivesRestart(t *testing.T) {
	state := map[string]*AvailabilityState{}
	NewAvailability(state, 1).Update("http:api", false, 0, "", 0)
	if got := NewAvailability(state, 1).DownNames(); !reflect.DeepEqual(got, []string{"http:api"}) {
		t.Fatal(got)
	}
	if event := NewAvailability(state, 1).Update("http:api", true, 90, "", 0); event == nil || event.Kind != Up {
		t.Fatal(event)
	}
}

func TestFormatDuration(t *testing.T) {
	seconds := 3725.0
	if formatDuration(&seconds) != "1 h 2 min" || formatDuration(nil) != "?" {
		t.Fatal("duración inesperada")
	}
}

var checkValues = map[string]string{
	"net.1.Gateway.IPAddress": "192.0.2.1",
	"p2p.1.Enable":            "true",
	"p2p.1.State":             "internet-ready",
	"camera.1.IP":             "192.0.2.14",
	"camstatus.1.LoginStatus": "connect success",
	"camstatus.2.LoginStatus": "disconnected",
}

func TestGatewayCheck(t *testing.T) {
	if got := gatewayCheck(checkValues, "192.0.2.8"); got == nil || got.OK {
		t.Fatal(got)
	}
	if got := gatewayCheck(checkValues, "192.0.2.1"); got == nil || !got.OK {
		t.Fatal(got)
	}
	if gatewayCheck(checkValues, "") != nil {
		t.Fatal("sin gateway esperado no hay comprobación")
	}
}

func TestP2POnlineDetected(t *testing.T) {
	if got := p2pCheck(checkValues, true, []string{"internet-ready"}); got == nil || got.OK {
		t.Fatal(got)
	}
	if p2pCheck(checkValues, false, []string{"internet-ready"}) != nil {
		t.Fatal("P2P desactivado debe omitirse")
	}
	offline := cloneValues(checkValues)
	offline["p2p.1.State"] = "offline"
	if got := p2pCheck(offline, true, []string{"internet-ready"}); got == nil || !got.OK {
		t.Fatal(got)
	}
}

func TestCamerasOnlyRegisteredChannels(t *testing.T) {
	got := cameraChecks(checkValues)
	want := []CheckResult{{Name: "camera:1", OK: true, Detail: "LoginStatus='connect success'"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cameraChecks = %#v", got)
	}
}

func TestCameraWeakPasswordIsOnline(t *testing.T) {
	values := cloneValues(checkValues)
	values["camstatus.1.LoginStatus"] = "weak password"
	if got := cameraChecks(values); len(got) != 1 || !got[0].OK {
		t.Fatalf("weak password debe indicar cámara conectada: %#v", got)
	}
}

func TestBehaviourChecksWithDrift(t *testing.T) {
	cfg := Config{TimeDriftMax: 60, ExpectedGateway: "192.0.2.8"}
	got := map[string]bool{}
	for _, result := range behaviourChecks(checkValues, floatPointer(300), cfg) {
		got[result.Name] = result.OK
	}
	want := map[string]bool{"time": false, "gateway": false, "camera:1": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestHTTPDateDrift(t *testing.T) {
	headers := map[string]string{"date": "Mon, 26 Oct 2026 10:00:30 GMT"}
	if got := httpDateDrift(headers, 1793008800); got == nil || *got != 30 {
		t.Fatal(got)
	}
	if httpDateDrift(nil, 0) != nil || httpDateDrift(map[string]string{"date": "basura"}, 0) != nil {
		t.Fatal("cabecera inválida debe omitirse")
	}
}

func floatPointer(value float64) *float64 { return &value }
