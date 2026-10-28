package egresswatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/statefile"
)

func TestPythonStateIsReadable(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "egresswatch", "python-state.json")
	var state WatchState
	found, err := statefile.Load(path, &state)
	if err != nil || !found {
		t.Fatalf("Load() = %v, %v", found, err)
	}
	if state.Aggregate.Daily.FwdPackets != 1 || state.Aggregate.Daily.FwdBytes != 60 || state.Aggregate.Daily.DropPackets != 1 {
		t.Fatalf("contadores = %+v", state.Aggregate.Daily)
	}
	if state.Aggregate.DNSMap["203.0.113.9"] != "erp.zwcloud.wang" || len(state.Tails) != 2 {
		t.Fatalf("estado Python = %+v", state)
	}
	if state.Throttle.Last["new:4294af1cc9b6c6cf"] != 1793010000 || len(state.Throttle.Sent) != 1 {
		t.Fatalf("throttle = %+v", state.Throttle)
	}
}

func TestPollProcessesDNSBeforeFlowsAndSavesState(t *testing.T) {
	dir := t.TempDir()
	dnsPath := filepath.Join(dir, "dns.log")
	ulogPath := filepath.Join(dir, "ulog.json")
	if err := os.WriteFile(dnsPath, []byte("Oct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 query[A] erp.zwcloud.wang from 192.168.0.227\nOct 26 10:00:00 dnsmasq[1]: 12 192.168.0.227/41234 reply erp.zwcloud.wang is 203.0.113.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ulogPath, []byte(`{"oob.prefix":"opennvr-fwd-out","src_ip":"192.168.0.227","dest_ip":"203.0.113.9","src_port":40000,"dest_port":8090,"ip.protocol":6,"ip.totlen":60}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Aggregate: defaultAggregateConfig(), UlogFiles: []string{ulogPath}, DNSFile: dnsPath, DataDir: filepath.Join(dir, "data"), PollSeconds: .01, LogMaxBytes: 1_000_000, NotifyMinInterval: 3600, NotifyMaxPerHour: 20}
	now := 1000.0
	watch, err := New(cfg, func() float64 { return now }, func() time.Time { return morning })
	if err != nil {
		t.Fatal(err)
	}
	if err := watch.Poll(); err != nil {
		t.Fatal(err)
	}
	if watch.State.Aggregate.DNSMap["203.0.113.9"] != "erp.zwcloud.wang" || watch.State.Aggregate.Daily.FwdBytes != 60 {
		t.Fatalf("estado = %+v", watch.State.Aggregate)
	}
	if watch.State.Throttle.Last["new:bbaf85c11e040454"] != now {
		t.Fatalf("clave de aviso = %+v", watch.State.Throttle.Last)
	}
	if !strings.Contains(watch.Summary(), "PERMITIDO hacia 203.0.113.9:8090/tcp (erp.zwcloud.wang)") {
		t.Fatal(watch.Summary())
	}
	var saved WatchState
	if found, err := statefile.Load(filepath.Join(cfg.DataDir, "state.json"), &saved); err != nil || !found || saved.Aggregate.Daily.FwdBytes != 60 {
		t.Fatalf("estado guardado = %v, %v, %+v", found, err, saved)
	}
}

func TestLoadConfigMatchesPythonDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("NVR_HOST", "192.0.2.10")
	t.Setenv("ULOG_FILES", "/a, /b")
	t.Setenv("POLL_SECONDS", "2.5")
	t.Setenv("SPIKE_FACTOR", "7.5")
	t.Setenv("NTFY_TOKEN", "token-prueba")
	cfg := LoadConfig()
	if cfg.Aggregate.NVRIP != "192.0.2.10" || cfg.Aggregate.SpikeFactor != 7.5 || cfg.PollSeconds != 2.5 || len(cfg.UlogFiles) != 2 || cfg.NTFYToken != "token-prueba" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.DNSFile != "/var/log/dnsmasq/queries.log" || cfg.Aggregate.EWMAAlpha != .2 {
		t.Fatalf("defaults = %+v", cfg)
	}
}
