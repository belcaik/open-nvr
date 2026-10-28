package egresswatch

import (
	"strings"
	"testing"
	"time"
)

const testNVR = "192.168.0.227"

var morning = time.Date(2026, 10, 26, 7, 0, 0, 0, time.Local)

func outbound(dst string, dport, length int, action, proto string) Flow {
	return Flow{Action: action, Direction: "out", Src: testNVR, Dst: dst, Proto: proto, Sport: 40000, Dport: dport, Length: length}
}

func response(src string, sport, length int) Flow {
	return Flow{Action: "fwd", Direction: "in", Src: src, Dst: testNVR, Proto: "tcp", Sport: sport, Dport: 40000, Length: length}
}

func newTestAggregator(overrides ...func(*AggregateConfig)) *Aggregator {
	cfg := AggregateConfig{NVRIP: testNVR, WindowSeconds: 300, SpikeFactor: 5, SpikeMinBytes: 1000, EWMAAlpha: .2, SummaryHour: 8, SilenceHours: 1}
	for _, override := range overrides {
		override(&cfg)
	}
	return NewAggregator(&AggregateState{}, cfg, 0, "2026-10-26")
}

func alertsWithPrefix(alerts []Alert, prefix string) []Alert {
	var found []Alert
	for _, alert := range alerts {
		if strings.HasPrefix(alert.Key, prefix) {
			found = append(found, alert)
		}
	}
	return found
}

func TestNewDestinationsAreBatchedOnce(t *testing.T) {
	agg := newTestAggregator()
	agg.Flow(outbound("203.0.113.5", 8090, 100, "drop", "tcp"), 1)
	agg.Flow(outbound("203.0.113.5", 8090, 100, "drop", "tcp"), 2)
	agg.Flow(outbound("203.0.113.6", 32100, 100, "drop", "udp"), 3)
	alerts := alertsWithPrefix(agg.Tick(4, morning), "new:")
	if len(alerts) != 1 || !strings.Contains(alerts[0].Title, "2 destinos nuevos") || !strings.Contains(alerts[0].Message, "bloqueado hacia 203.0.113.5:8090/tcp") {
		t.Fatalf("alertas = %+v", alerts)
	}
	agg.Flow(outbound("203.0.113.5", 8090, 100, "drop", "tcp"), 5)
	if got := alertsWithPrefix(agg.Tick(6, morning), "new:"); len(got) != 0 {
		t.Fatalf("destino repetido = %+v", got)
	}
}

func TestReplyCountsAsSameFlow(t *testing.T) {
	if FlowKey(outbound("203.0.113.5", 443, 100, "fwd", "tcp")) != FlowKey(response("203.0.113.5", 443, 100)) {
		t.Fatal("la respuesta debe compartir la clave del flujo saliente")
	}
}

func TestDomainFromDNSAndNewDomain(t *testing.T) {
	agg := newTestAggregator()
	agg.DNS(DNS{Kind: "query", Name: "erp.zwcloud.wang", Client: testNVR, QType: "A"}, 1)
	agg.DNS(DNS{Kind: "reply", Name: "erp.zwcloud.wang", Value: "203.0.113.9"}, 1)
	agg.DNS(DNS{Kind: "query", Name: "other.example", Client: "192.168.0.50", QType: "A"}, 1)
	agg.Flow(outbound("203.0.113.9", 8090, 100, "drop", "tcp"), 2)
	message := agg.Tick(3, morning)[0].Message
	if !strings.Contains(message, "DNS consulta erp.zwcloud.wang") || !strings.Contains(message, "203.0.113.9:8090/tcp (erp.zwcloud.wang)") || strings.Contains(message, "other.example") {
		t.Fatal(message)
	}
}

func TestSpikeDetection(t *testing.T) {
	agg := newTestAggregator()
	for window := 0; window < 5; window++ {
		agg.Flow(outbound("203.0.113.5", 443, 200, "fwd", "tcp"), float64(window*300+10))
		agg.Tick(float64(window*300+20), morning)
	}
	agg.Tick(1501, morning)
	agg.Flow(outbound("203.0.113.5", 443, 50000, "fwd", "tcp"), 1510)
	spikes := alertsWithPrefix(agg.Tick(1801, morning), "spike")
	if len(spikes) != 1 || !strings.Contains(spikes[0].Message, "203.0.113.5:443/tcp") {
		t.Fatalf("picos = %+v", spikes)
	}
}

func TestDroppedTrafficNeverSpikes(t *testing.T) {
	agg := newTestAggregator()
	agg.Flow(outbound("203.0.113.5", 443, 10_000_000, "drop", "tcp"), 10)
	if got := alertsWithPrefix(agg.Tick(301, morning), "spike"); len(got) != 0 {
		t.Fatalf("picos = %+v", got)
	}
}

func TestLongGapDoesNotLoopForever(t *testing.T) {
	agg := newTestAggregator()
	agg.Tick(10*86400, morning)
	if agg.State.Window.Start != 10*86400 {
		t.Fatalf("inicio = %v", agg.State.Window.Start)
	}
}

func TestSilenceAlertAndResume(t *testing.T) {
	agg := newTestAggregator()
	alerts := agg.Tick(3601, morning)
	if len(alerts) != 1 || alerts[0].Key != "silence" {
		t.Fatalf("alertas = %+v", alerts)
	}
	if got := alertsWithPrefix(agg.Tick(7200, morning), "silence"); len(got) != 0 {
		t.Fatalf("silencio repetido = %+v", got)
	}
	alerts = agg.Flow(outbound("203.0.113.5", 1, 100, "drop", "tcp"), 7300)
	if len(alerts) != 1 || alerts[0].Key != "silence:end" {
		t.Fatalf("reanudar = %+v", alerts)
	}
}

func TestDailySummaryOnceAfterHour(t *testing.T) {
	agg := newTestAggregator()
	agg.Flow(outbound("203.0.113.5", 8090, 100, "drop", "tcp"), 1)
	agg.DNS(DNS{Kind: "query", Name: "utilerp.p6sai.com", Client: testNVR, QType: "A"}, 1)
	agg.DNS(DNS{Kind: "config", Name: "utilerp.p6sai.com", Value: "NXDOMAIN"}, 1)
	if got := alertsWithPrefix(agg.Tick(2, morning), "summary:"); len(got) != 0 {
		t.Fatalf("resumen temprano = %+v", got)
	}
	nine := time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local)
	tomorrow := nine.AddDate(0, 0, 1)
	if got := alertsWithPrefix(agg.Tick(3, nine), "summary:"); len(got) != 0 {
		t.Fatalf("resumen mismo día = %+v", got)
	}
	summaries := alertsWithPrefix(agg.Tick(4, tomorrow), "summary:")
	if len(summaries) != 1 || !strings.Contains(summaries[0].Message, "Bloqueado: 1 paquetes") || !strings.Contains(summaries[0].Message, "utilerp.p6sai.com  (NXDOMAIN)") {
		t.Fatalf("resúmenes = %+v", summaries)
	}
	if got := alertsWithPrefix(agg.Tick(5, tomorrow), "summary:"); len(got) != 0 || agg.State.Daily.DropPackets != 0 {
		t.Fatalf("segundo resumen = %+v; diario = %+v", got, agg.State.Daily)
	}
}
