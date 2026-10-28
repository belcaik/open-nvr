package nvrwatch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type handlerProbes struct{ handler http.Handler }

func (p handlerProbes) TCP(string, int) (bool, string) { return true, "abierto" }
func (p handlerProbes) RTSPDescribe(string, int, string, string, string) (bool, string) {
	return true, "RTSP 200"
}
func (p handlerProbes) Scan(string, []int, func(time.Duration)) []int { return nil }
func (p handlerProbes) HTTPGet(_ string, _ int, path, user, password string) (int, map[string]string, []byte, error) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	recorder := httptest.NewRecorder()
	p.handler.ServeHTTP(recorder, req)
	result := recorder.Result()
	headers := map[string]string{}
	for key, values := range result.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[0]
		}
	}
	return result.StatusCode, headers, recorder.Body.Bytes(), nil
}

func TestPythonStateAndBaselineCompatibility(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "..", "testdata")
	dataDir := t.TempDir()
	copyFixture(t, filepath.Join(fixtureDir, "nvrwatch", "python-state.json"), filepath.Join(dataDir, "state.json"))
	copyFixture(t, filepath.Join(fixtureDir, "nvrwatch", "python-baseline.json"), filepath.Join(dataDir, "baseline.json"))

	var before map[string]json.RawMessage
	loadJSON(t, filepath.Join(dataDir, "state.json"), &before)
	var baseline Baseline
	loadJSON(t, filepath.Join(dataDir, "baseline.json"), &baseline)
	if baseline.Created != 1793008800.5 || len(baseline.Sections) != 2 || len(baseline.Values) == 0 {
		t.Fatalf("baseline Python no compatible: %#v", baseline)
	}

	routes := map[string]string{
		"/System/DeviceInfo":                   "device_info.xml",
		"/System/FrontConnectDevicetList":      "front_connect_list.xml",
		"/System/FrontConnectDeviceStatusList": "front_connect_status.xml",
		"/Network/P2PV2":                       "p2p.xml",
		"/System/NTP":                          "ntp.xml",
		"/Network/Interfaces":                  "network_interfaces.xml",
		"/System/DeviceStatus":                 "device_status.xml",
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(fixtureDir, "nvr", name))
	})
	cfg := defaultConfig()
	cfg.DataDir = dataDir
	cfg.KnownPorts = nil
	cfg.ScanEnabled = false
	cfg.BaselineInterval = 1 << 30
	app, err := NewApp(cfg, nil, func() float64 { return 1793008801.5 }, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	app.probes = handlerProbes{handler: handler}
	if err := app.Tick(1793008801.5); err != nil {
		t.Fatal(err)
	}
	var after map[string]json.RawMessage
	loadJSON(t, filepath.Join(dataDir, "state.json"), &after)
	for key := range before {
		if _, ok := after[key]; !ok {
			t.Errorf("Tick perdió la clave de estado %q", key)
		}
	}
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
