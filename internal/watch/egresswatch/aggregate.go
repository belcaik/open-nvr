package egresswatch

import (
	"crypto/sha256"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

const (
	maxKnown     = 20000
	maxDailyKeys = 5000
	maxDNSMap    = 5000
	maxListed    = 30
)

type AggregateConfig struct {
	NVRIP         string
	WindowSeconds int
	SpikeFactor   float64
	SpikeMinBytes int
	EWMAAlpha     float64
	SummaryHour   int
	SilenceHours  float64
}

type Alert struct {
	Key, Title, Message string
	Priority            int
	Tags                []string
	Interval            float64
}

type DailyState struct {
	Flows       map[string][2]int `json:"flows"`
	DNS         map[string]int    `json:"dns"`
	DNSNX       map[string]int    `json:"dns_nx"`
	New         int               `json:"new"`
	FwdPackets  int               `json:"fwd_packets"`
	FwdBytes    int               `json:"fwd_bytes"`
	DropPackets int               `json:"drop_packets"`
}

type WindowState struct {
	Start   float64        `json:"start"`
	Bytes   int            `json:"bytes"`
	Packets int            `json:"packets"`
	ByKey   map[string]int `json:"by_key"`
}

type AggregateState struct {
	Known           map[string]float64 `json:"known"`
	PendingNew      []string           `json:"pending_new"`
	DNSMap          map[string]string  `json:"dnsmap"`
	Window          WindowState        `json:"window"`
	EWMA            *float64           `json:"ewma"`
	Daily           DailyState         `json:"daily"`
	LastEvent       float64            `json:"last_event"`
	SilenceAlerted  bool               `json:"silence_alerted"`
	LastSummaryDate string             `json:"last_summary_date"`
}

type Aggregator struct {
	State  *AggregateState
	Config AggregateConfig
}

func emptyDaily() DailyState {
	return DailyState{Flows: map[string][2]int{}, DNS: map[string]int{}, DNSNX: map[string]int{}}
}

func NewAggregator(state *AggregateState, cfg AggregateConfig, now float64, today string) *Aggregator {
	if state.Known == nil {
		state.Known = map[string]float64{}
	}
	if state.PendingNew == nil {
		state.PendingNew = []string{}
	}
	if state.DNSMap == nil {
		state.DNSMap = map[string]string{}
	}
	if state.Window.ByKey == nil {
		state.Window = WindowState{Start: now, ByKey: map[string]int{}}
	}
	if state.Daily.Flows == nil {
		state.Daily.Flows = map[string][2]int{}
	}
	if state.Daily.DNS == nil {
		state.Daily.DNS = map[string]int{}
	}
	if state.Daily.DNSNX == nil {
		state.Daily.DNSNX = map[string]int{}
	}
	if state.LastEvent == 0 {
		state.LastEvent = now
	}
	if state.LastSummaryDate == "" {
		state.LastSummaryDate = today
	}
	return &Aggregator{State: state, Config: cfg}
}

// FlowKey reúne las respuestas permitidas con el flujo saliente que las originó.
func FlowKey(flow Flow) string {
	if flow.Direction == "in" {
		if flow.Action == "fwd" {
			return fmt.Sprintf("fwd|out|%s|%d|%s", flow.Src, flow.Sport, flow.Proto)
		}
		return fmt.Sprintf("drop|in|%s|%d|%s", flow.Src, flow.Dport, flow.Proto)
	}
	return fmt.Sprintf("%s|%s|%s|%d|%s", flow.Action, flow.Direction, flow.Dst, flow.Dport, flow.Proto)
}

func deleteOne[V any](values map[string]V) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		delete(values, keys[0])
	}
}

func (a *Aggregator) remember(key string, now float64) {
	if _, exists := a.State.Known[key]; exists {
		return
	}
	if len(a.State.Known) >= maxKnown {
		deleteOne(a.State.Known)
	}
	a.State.Known[key] = now
	a.State.PendingNew = append(a.State.PendingNew, key)
	a.State.Daily.New++
}

func (a *Aggregator) Flow(flow Flow, now float64) []Alert {
	var alerts []Alert
	a.State.LastEvent = now
	if a.State.SilenceAlerted {
		a.State.SilenceAlerted = false
		alerts = append(alerts, Alert{Key: "silence:end", Title: "vuelve a haber tráfico del NVR registrado", Message: "egress-watch vuelve a recibir paquetes del NVR.", Priority: 2, Tags: []string{"white_check_mark"}, Interval: 0})
	}
	key := FlowKey(flow)
	a.remember(key, now)
	daily := &a.State.Daily
	if _, exists := daily.Flows[key]; exists || len(daily.Flows) < maxDailyKeys {
		entry := daily.Flows[key]
		entry[0]++
		entry[1] += flow.Length
		daily.Flows[key] = entry
	}
	if flow.Action == "fwd" {
		daily.FwdPackets++
		daily.FwdBytes += flow.Length
		a.State.Window.Bytes += flow.Length
		a.State.Window.Packets++
		a.State.Window.ByKey[key] += flow.Length
	} else {
		daily.DropPackets++
	}
	return alerts
}

func (a *Aggregator) DNS(event DNS, now float64) []Alert {
	switch event.Kind {
	case "query":
		if a.Config.NVRIP != "" && event.Client != "" && event.Client != a.Config.NVRIP {
			return nil
		}
		if _, exists := a.State.Daily.DNS[event.Name]; exists || len(a.State.Daily.DNS) < maxDailyKeys {
			a.State.Daily.DNS[event.Name]++
		}
		a.remember("dns|"+event.Name, now)
	case "reply", "cached":
		if event.Value != "" && net.ParseIP(event.Value) != nil {
			delete(a.State.DNSMap, event.Value)
			if len(a.State.DNSMap) >= maxDNSMap {
				deleteOne(a.State.DNSMap)
			}
			a.State.DNSMap[event.Value] = event.Name
		}
	case "config":
		if event.Value == "NXDOMAIN" {
			if _, exists := a.State.Daily.DNSNX[event.Name]; exists || len(a.State.Daily.DNSNX) < maxDailyKeys {
				a.State.Daily.DNSNX[event.Name]++
			}
		}
	}
	return nil
}

func (a *Aggregator) Describe(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) >= 2 && parts[0] == "dns" {
		return "DNS consulta " + parts[1]
	}
	if len(parts) != 5 {
		return key
	}
	action, direction, ip, port, proto := parts[0], parts[1], parts[2], parts[3], parts[4]
	where := ip + ":" + port + "/" + proto
	if domain := a.State.DNSMap[ip]; domain != "" {
		where += " (" + domain + ")"
	}
	switch direction {
	case "in":
		return "bloqueado ENTRANTE desde " + where
	case "v6":
		return "bloqueado IPv6 hacia " + where
	case "spoof":
		return "bloqueado, MAC del NVR con otra IP, hacia " + where
	}
	if action == "fwd" {
		return "PERMITIDO hacia " + where
	}
	return "bloqueado hacia " + where
}

func (a *Aggregator) Tick(now float64, localNow time.Time) []Alert {
	var alerts []Alert
	alerts = append(alerts, a.newDestinations()...)
	alerts = append(alerts, a.closeWindows(now)...)
	alerts = append(alerts, a.silence(now)...)
	alerts = append(alerts, a.summary(localNow)...)
	return alerts
}

func (a *Aggregator) newDestinations() []Alert {
	pending := a.State.PendingNew
	if len(pending) == 0 {
		return nil
	}
	a.State.PendingNew = []string{}
	lines := make([]string, 0, min(len(pending), maxListed)+1)
	for _, key := range pending[:min(len(pending), maxListed)] {
		lines = append(lines, a.Describe(key))
	}
	if len(pending) > maxListed {
		lines = append(lines, fmt.Sprintf("... y %d más", len(pending)-maxListed))
	}
	allowed := false
	for _, key := range pending {
		allowed = allowed || strings.HasPrefix(key, "fwd|")
	}
	sorted := append([]string(nil), pending...)
	sort.Strings(sorted)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(sorted, "\n"))))[:16]
	priority := 3
	if allowed {
		priority = 4
	}
	return []Alert{{Key: "new:" + digest, Title: fmt.Sprintf("%d destinos nuevos del NVR", len(pending)), Message: strings.Join(lines, "\n"), Priority: priority, Tags: []string{"mag"}, Interval: -1}}
}

func humanBytes(value float64) string {
	for _, unit := range []string{"B", "KiB", "MiB", "GiB"} {
		if value < 1024 || unit == "GiB" {
			if unit == "B" {
				return fmt.Sprintf("%.0f %s", value, unit)
			}
			return fmt.Sprintf("%.1f %s", value, unit)
		}
		value /= 1024
	}
	return fmt.Sprint(value)
}

func (a *Aggregator) closeWindows(now float64) []Alert {
	var alerts []Alert
	size := a.Config.WindowSeconds
	window := a.State.Window
	closed := 0
	for size > 0 && now >= window.Start+float64(size) {
		bytes := window.Bytes
		if bytes >= a.Config.SpikeMinBytes && (a.State.EWMA == nil || float64(bytes) > a.Config.SpikeFactor**a.State.EWMA) {
			type pair struct {
				key   string
				bytes int
			}
			top := make([]pair, 0, len(window.ByKey))
			for key, count := range window.ByKey {
				top = append(top, pair{key, count})
			}
			sort.Slice(top, func(i, j int) bool {
				if top[i].bytes == top[j].bytes {
					return top[i].key < top[j].key
				}
				return top[i].bytes > top[j].bytes
			})
			if len(top) > 5 {
				top = top[:5]
			}
			lines := make([]string, len(top))
			for i, item := range top {
				lines[i] = fmt.Sprintf("%s  %s", humanBytes(float64(item.bytes)), a.Describe(item.key))
			}
			average := "sin historial"
			if a.State.EWMA != nil {
				average = humanBytes(*a.State.EWMA)
			}
			alerts = append(alerts, Alert{Key: "spike", Title: fmt.Sprintf("pico de tráfico permitido: %s en %d min", humanBytes(float64(bytes)), size/60), Message: fmt.Sprintf("%d paquetes; media móvil %s.\n%s", window.Packets, average, strings.Join(lines, "\n")), Priority: 4, Tags: []string{"chart_with_upwards_trend"}, Interval: 3600})
		}
		value := float64(bytes)
		if a.State.EWMA != nil {
			value = a.Config.EWMAAlpha*value + (1-a.Config.EWMAAlpha)**a.State.EWMA
		}
		a.State.EWMA = &value
		closed++
		if closed >= 288 {
			window = WindowState{Start: now, ByKey: map[string]int{}}
			break
		}
		window = WindowState{Start: window.Start + float64(size), ByKey: map[string]int{}}
	}
	a.State.Window = window
	return alerts
}

func (a *Aggregator) silence(now float64) []Alert {
	if a.Config.SilenceHours <= 0 || a.State.SilenceAlerted {
		return nil
	}
	idle := now - a.State.LastEvent
	if idle < a.Config.SilenceHours*3600 {
		return nil
	}
	a.State.SilenceAlerted = true
	return []Alert{{Key: "silence", Title: fmt.Sprintf("sin tráfico del NVR registrado en %.1f h", idle/3600), Message: "Posibles causas: el NVR ya no usa Baphomet como puerta de enlace (¿DHCP, reinicio, cambio de configuración?), la tabla nft `inet opennvr` no está cargada o ulogd2 no está registrando.", Priority: 4, Tags: []string{"warning"}, Interval: -1}}
}

func (a *Aggregator) summary(localNow time.Time) []Alert {
	today := localNow.Format("2006-01-02")
	if localNow.Hour() < a.Config.SummaryHour || a.State.LastSummaryDate == today {
		return nil
	}
	daily := a.State.Daily
	a.State.Daily = emptyDaily()
	a.State.LastSummaryDate = today
	return []Alert{{Key: "summary:" + today, Title: "resumen diario de tráfico del NVR", Message: a.FormatSummary(daily), Priority: 2, Tags: []string{"bar_chart"}, Interval: -1}}
}

func (a *Aggregator) FormatSummary(daily DailyState) string {
	lines := []string{fmt.Sprintf("Permitido: %d paquetes, %s", daily.FwdPackets, humanBytes(float64(daily.FwdBytes))), fmt.Sprintf("Bloqueado: %d paquetes", daily.DropPackets), fmt.Sprintf("Destinos/dominios nuevos: %d", daily.New)}
	type flowPair struct {
		key   string
		value [2]int
	}
	var fwd, drop []flowPair
	for key, value := range daily.Flows {
		if strings.HasPrefix(key, "fwd|") {
			fwd = append(fwd, flowPair{key, value})
		} else {
			drop = append(drop, flowPair{key, value})
		}
	}
	sort.Slice(fwd, func(i, j int) bool {
		if fwd[i].value[1] == fwd[j].value[1] {
			return fwd[i].key < fwd[j].key
		}
		return fwd[i].value[1] > fwd[j].value[1]
	})
	sort.Slice(drop, func(i, j int) bool {
		if drop[i].value[0] == drop[j].value[0] {
			return drop[i].key < drop[j].key
		}
		return drop[i].value[0] > drop[j].value[0]
	})
	if len(fwd) > 10 {
		fwd = fwd[:10]
	}
	if len(drop) > 10 {
		drop = drop[:10]
	}
	if len(fwd) > 0 {
		lines = append(lines, "\nPermitidos (por bytes):")
		for _, item := range fwd {
			lines = append(lines, fmt.Sprintf("  %s  %d pq  %s", humanBytes(float64(item.value[1])), item.value[0], a.Describe(item.key)))
		}
	}
	if len(drop) > 0 {
		lines = append(lines, "\nBloqueados (por paquetes):")
		for _, item := range drop {
			lines = append(lines, fmt.Sprintf("  %d pq  %s", item.value[0], a.Describe(item.key)))
		}
	}
	type dnsPair struct {
		name  string
		count int
	}
	dns := make([]dnsPair, 0, len(daily.DNS))
	for name, count := range daily.DNS {
		dns = append(dns, dnsPair{name, count})
	}
	sort.Slice(dns, func(i, j int) bool {
		if dns[i].count == dns[j].count {
			return dns[i].name < dns[j].name
		}
		return dns[i].count > dns[j].count
	})
	if len(dns) > 10 {
		dns = dns[:10]
	}
	if len(dns) > 0 {
		lines = append(lines, "\nDNS más consultados:")
		for _, item := range dns {
			suffix := ""
			if _, ok := daily.DNSNX[item.name]; ok {
				suffix = "  (NXDOMAIN)"
			}
			lines = append(lines, fmt.Sprintf("  %d  %s%s", item.count, item.name, suffix))
		}
	}
	return strings.Join(lines, "\n")
}
