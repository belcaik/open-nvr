package nvrwatch

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/statefile"
)

func TestAppBaselineFullCycle(t *testing.T) {
	dataDir := t.TempDir()
	cfg := defaultConfig()
	cfg.DataDir = dataDir
	cfg.BaselineRealert = 86400
	var sent []notify.Payload
	notifier := &notify.Notifier{URL: "https://ntfy.example", Topic: "t", Prefix: "NVR: ", Throttle: notify.NewThrottle(&notify.ThrottleState{}, 3600, 100), Send: func(_ string, payload notify.Payload, _ string) error {
		sent = append(sent, payload)
		return nil
	}}
	app, err := NewApp(cfg, notifier, func() float64 { return 0 }, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	old := logx.Out
	logx.Out = &output
	t.Cleanup(func() { logx.Out = old })

	v1 := map[string]string{"device.SoftWareVersion": "V1", "p2p.1.Enable": "true"}
	if changes, err := app.compareBaseline(v1, []string{"device", "p2p"}, 0); err != nil || len(changes) != 0 {
		t.Fatalf("primera comparación = %#v, %v", changes, err)
	}
	if len(sent) != 1 || sent[0].Title != "NVR: línea base creada" {
		t.Fatal(sent)
	}

	v2 := cloneValues(v1)
	v2["port.80"] = "open"
	if changes, err := app.compareBaseline(v2, []string{"device", "p2p", "port"}, 100); err != nil || len(changes) != 0 || len(sent) != 1 {
		t.Fatalf("sección nueva = %#v, %v, avisos=%d", changes, err, len(sent))
	}

	v3 := cloneValues(v2)
	v3["p2p.1.Enable"] = "false"
	v3["port.23"] = "open"
	changes, err := app.compareBaseline(v3, []string{"device", "p2p", "port"}, 200)
	if err != nil || len(changes) != 2 || len(sent) != 2 || !strings.Contains(sent[1].Message, "p2p.1.Enable") {
		t.Fatalf("cambio = %#v, %v, avisos=%#v", changes, err, sent)
	}
	if _, err := app.compareBaseline(v3, []string{"device", "p2p", "port"}, 1100); err != nil || len(sent) != 2 {
		t.Fatalf("dedup = %v, avisos=%d", err, len(sent))
	}

	snapshot := Snapshot{TS: 1100, Sections: []string{"device", "p2p", "port"}, Values: v3}
	if err := statefile.Save(filepath.Join(dataDir, "last_snapshot.json"), snapshot); err != nil {
		t.Fatal(err)
	}
	if code := Accept(cfg); code != 0 {
		t.Fatalf("Accept = %d", code)
	}
	if changes, err := app.compareBaseline(v3, []string{"device", "p2p", "port"}, 1200); err != nil || len(changes) != 0 {
		t.Fatalf("después de aceptar = %#v, %v", changes, err)
	}
}

func TestRunForeverStopsOnCanceledContext(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := NewApp(cfg, nil, func() float64 { return 0 }, func(time.Duration) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		app.RunForever(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunForever no respetó la cancelación")
	}
}
