package nvrwatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/health"
	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/statefile"
)

type ScanState struct {
	TS   float64 `json:"ts"`
	Open []int   `json:"open"`
}

type State struct {
	Avail        map[string]*AvailabilityState `json:"avail"`
	Throttle     notify.ThrottleState          `json:"throttle"`
	Scan         *ScanState                    `json:"scan,omitempty"`
	LastScan     float64                       `json:"last_scan"`
	LastBaseline float64                       `json:"last_baseline"`
}

type Baseline struct {
	Created  float64           `json:"created"`
	Accepted *float64          `json:"accepted,omitempty"`
	Sections []string          `json:"sections"`
	Values   map[string]string `json:"values"`
}

type Snapshot struct {
	TS       float64           `json:"ts"`
	Sections []string          `json:"sections"`
	Values   map[string]string `json:"values"`
}

type App struct {
	cfg                                   Config
	statePath, baselinePath, snapshotPath string
	state                                 State
	availability                          *Availability
	throttle                              *notify.Throttle
	notifier                              *notify.Notifier
	clock                                 func() float64
	sleep                                 func(time.Duration)
	probes                                Probes
}

func NewApp(cfg Config, notifier *notify.Notifier, clock func() float64, sleep func(time.Duration)) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = func() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	a := &App{cfg: cfg, statePath: filepath.Join(cfg.DataDir, "state.json"), baselinePath: filepath.Join(cfg.DataDir, "baseline.json"), snapshotPath: filepath.Join(cfg.DataDir, "last_snapshot.json"), clock: clock, sleep: sleep, probes: networkProbes{}}
	if found, err := statefile.Load(a.statePath, &a.state); err != nil {
		return nil, err
	} else if !found {
		a.state = State{}
	}
	if a.state.Avail == nil {
		a.state.Avail = map[string]*AvailabilityState{}
	}
	a.availability = NewAvailability(a.state.Avail, cfg.FailThreshold)
	a.throttle = notify.NewThrottle(&a.state.Throttle, float64(cfg.NotifyMinInterval), cfg.NotifyMaxPerHour)
	if notifier == nil {
		notifier = &notify.Notifier{URL: cfg.NtfyURL, Topic: cfg.NtfyTopic, Token: cfg.NtfyToken, Prefix: "NVR: ", Throttle: a.throttle}
	}
	a.notifier = notifier
	return a, nil
}

func (a *App) compareBaseline(values map[string]string, sections []string, now float64) ([]Change, error) {
	var base Baseline
	found, err := statefile.Load(a.baselinePath, &base)
	if err != nil {
		return nil, err
	}
	sections = uniqueSorted(sections)
	if !found {
		base = Baseline{Created: now, Sections: sections, Values: values}
		if err := statefile.Save(a.baselinePath, base); err != nil {
			return nil, err
		}
		a.notifier.Notify("baseline:new", "línea base creada", fmt.Sprintf("%d claves en %s.", len(values), joinComma(sections)), notify.PriorityLow, []string{"memo"}, -1, now)
		return nil, nil
	}
	newSections := difference(sections, base.Sections)
	if len(newSections) > 0 {
		base.Values = mergeBaseline(base.Values, values, newSections)
		base.Sections = uniqueSorted(append(base.Sections, newSections...))
		if err := statefile.Save(a.baselinePath, base); err != nil {
			return nil, err
		}
		logx.Printf("secciones añadidas a la línea base: %s", joinComma(newSections))
	}
	changes := diffBaseline(base.Values, values, difference(sections, newSections), a.cfg.IgnorePrefixes)
	if len(changes) > 0 {
		body := formatChanges(changes, 25) + "\n\nSi el cambio es esperado: podman exec opennvr-nvr-watch /opennvr nvr-watch accept"
		a.notifier.Notify("baseline:"+fingerprint(changes), fmt.Sprintf("%d cambios respecto a la línea base", len(changes)), body, notify.PriorityHigh, []string{"warning"}, float64(a.cfg.BaselineRealert), now)
	}
	return changes, nil
}

type endpoint struct {
	section, path string
	extract       func([]byte) (map[string]string, error)
}

var endpoints = []endpoint{
	{"device", "/System/DeviceInfo", deviceInfo},
	{"camera", "/System/FrontConnectDevicetList", cameras},
	{"camstatus", "/System/FrontConnectDeviceStatusList", cameraStatus},
	{"p2p", "/Network/P2PV2", p2p},
	{"ntp", "/System/NTP", ntpValues},
	{"net", "/Network/Interfaces", networkValues},
	{"status", "/System/DeviceStatus", deviceStatus},
}

func (a *App) apply(results []CheckResult, now float64, threshold int) []Event {
	var events []Event
	for _, result := range results {
		if event := a.availability.Update(result.Name, result.OK, now, result.Detail, threshold); event != nil {
			events = append(events, *event)
		}
	}
	return events
}

func (a *App) checkAvailability(now float64) []Event {
	var results []CheckResult
	for _, port := range a.cfg.KnownPorts {
		ok, detail := a.probes.TCP(a.cfg.Host, port)
		results = append(results, CheckResult{Name: fmt.Sprintf("tcp:%d", port), OK: ok, Detail: detail})
	}
	status, _, _, err := a.probes.HTTPGet(a.cfg.Host, a.cfg.HTTPPort, "/System/DeviceInfo", a.cfg.User, a.cfg.Password)
	if err != nil {
		results = append(results, CheckResult{Name: "http:api", Detail: fmt.Sprintf("%T", err)})
	} else {
		detail := fmt.Sprintf("HTTP %d", status)
		if status == 401 {
			detail += " (credenciales rechazadas)"
		}
		results = append(results, CheckResult{Name: "http:api", OK: status == 200, Detail: detail})
	}
	ok, detail := a.probes.RTSPDescribe(a.cfg.Host, a.cfg.RTSPPort, a.cfg.RTSPPath, a.cfg.User, a.cfg.Password)
	results = append(results, CheckResult{Name: "rtsp", OK: ok, Detail: detail})
	return a.apply(results, now, 0)
}

func (a *App) notifyEvents(events []Event, now float64) {
	var downs, ups []Event
	for _, event := range events {
		if event.Kind == Down {
			downs = append(downs, event)
		} else {
			ups = append(ups, event)
		}
	}
	if len(downs) > 0 {
		names, lines := make([]string, 0, len(downs)), make([]string, 0, len(downs))
		for _, event := range downs {
			names = append(names, event.Name)
			lines = append(lines, label(event.Name)+": "+event.Detail)
		}
		slices.Sort(names)
		title := "fallo: " + joinEventLabels(downs)
		tcpDown := true
		for _, port := range a.cfg.KnownPorts {
			if !slices.Contains(a.availability.DownNames(), fmt.Sprintf("tcp:%d", port)) {
				tcpDown = false
			}
		}
		if len(a.cfg.KnownPorts) > 0 && tcpDown {
			title = fmt.Sprintf("inaccesible (%s)", a.cfg.Host)
		}
		a.notifier.Notify("down:"+strings.Join(names, ","), title, strings.Join(lines, "\n"), notify.PriorityHigh, []string{"rotating_light"}, -1, now)
	}
	if len(ups) > 0 {
		names, lines := make([]string, 0, len(ups)), make([]string, 0, len(ups))
		for _, event := range ups {
			names = append(names, event.Name)
			lines = append(lines, fmt.Sprintf("%s: %s (caído %s)", label(event.Name), event.Detail, formatDuration(event.Duration)))
		}
		slices.Sort(names)
		a.notifier.Notify("up:"+strings.Join(names, ","), "recuperado: "+joinEventLabels(ups), strings.Join(lines, "\n"), notify.PriorityDefault, []string{"white_check_mark"}, -1, now)
	}
}

func joinEventLabels(events []Event) string {
	values := make([]string, len(events))
	for index, event := range events {
		values[index] = label(event.Name)
	}
	return strings.Join(values, ", ")
}

func (a *App) fetchSnapshot() (map[string]string, []string, *float64) {
	values := map[string]string{}
	var sections []string
	var drift *float64
	for _, endpoint := range endpoints {
		status, headers, body, err := a.probes.HTTPGet(a.cfg.Host, a.cfg.HTTPPort, endpoint.path, a.cfg.User, a.cfg.Password)
		if err != nil {
			logx.Printf("%s: %T", endpoint.path, err)
			continue
		}
		if endpoint.section == "device" {
			drift = httpDateDrift(headers, a.clock())
		}
		if status != 200 {
			logx.Printf("%s: HTTP %d", endpoint.path, status)
			continue
		}
		extracted, err := endpoint.extract(body)
		if err != nil {
			logx.Printf("%s: %v", endpoint.path, err)
			continue
		}
		for key, value := range extracted {
			values[key] = value
		}
		sections = append(sections, endpoint.section)
	}
	if a.state.Scan != nil {
		for _, port := range a.state.Scan.Open {
			values[fmt.Sprintf("port.%d", port)] = "open"
		}
		sections = append(sections, "port")
	}
	return values, uniqueSorted(sections), drift
}

func (a *App) checkBehaviour(now float64) error {
	values, sections, drift := a.fetchSnapshot()
	if err := statefile.Save(a.snapshotPath, Snapshot{TS: now, Sections: sections, Values: values}); err != nil {
		return err
	}
	a.notifyEvents(a.apply(behaviourChecks(values, drift, a.cfg), now, 0), now)
	if len(sections) == 0 {
		return nil
	}
	_, err := a.compareBaseline(values, sections, now)
	return err
}

func (a *App) runScan(now float64) {
	ports := scanPorts(append(append([]int(nil), a.cfg.KnownPorts...), a.cfg.ScanExtra...))
	logx.Printf("escaneo TCP connect de %d puertos", len(ports))
	open := a.probes.Scan(a.cfg.Host, ports, a.sleep)
	if len(open) == 0 {
		logx.Printf("escaneo sin puertos abiertos: NVR inaccesible, no se guarda")
		return
	}
	slices.Sort(open)
	a.state.Scan = &ScanState{TS: now, Open: open}
	logx.Printf("puertos abiertos: %v", open)
}

// Tick ejecuta un ciclo completo y persiste el estado compatible con Python.
func (a *App) Tick(now float64) error {
	a.notifyEvents(a.checkAvailability(now), now)
	nvrUp := !slices.Contains(a.availability.DownNames(), "http:api")
	if a.cfg.ScanEnabled && nvrUp && now-a.state.LastScan >= float64(a.cfg.ScanInterval) {
		a.runScan(now)
		a.state.LastScan = now
	}
	if nvrUp && now-a.state.LastBaseline >= float64(a.cfg.BaselineInterval) {
		if err := a.checkBehaviour(now); err != nil {
			return err
		}
		a.state.LastBaseline = now
	}
	a.throttle.Prune(now, 7*86400)
	return statefile.Save(a.statePath, a.state)
}

// Once ejecuta un ciclo sin escaneo de puertos y fuerza la línea base.
func (a *App) Once(now float64) error {
	a.state.LastScan = now
	a.state.LastBaseline = 0
	return a.Tick(now)
}

// RunForever mantiene el vigilante vivo hasta la cancelación del contexto.
func (a *App) RunForever(ctx context.Context) {
	logx.Printf("nvr-watch: NVR %s, intervalo %d s, línea base cada %d s, escaneo cada %d s", a.cfg.Host, a.cfg.CheckInterval, a.cfg.BaselineInterval, a.cfg.ScanInterval)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		start := a.clock()
		if err := health.Beat("/tmp/heartbeat"); err != nil {
			logx.Printf("error escribiendo latido: %v", err)
		}
		if err := a.Tick(start); err != nil {
			logx.Printf("error en el ciclo: %v", err)
		}
		delay := float64(a.cfg.CheckInterval) - (a.clock() - start)
		if delay < 1 {
			delay = 1
		}
		timer := time.NewTimer(time.Duration(delay * float64(time.Second)))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func Accept(cfg Config) int {
	var snapshot Snapshot
	found, err := statefile.Load(filepath.Join(cfg.DataDir, "last_snapshot.json"), &snapshot)
	if err != nil || !found {
		fmt.Println("no hay instantánea todavía")
		return 1
	}
	basePath := filepath.Join(cfg.DataDir, "baseline.json")
	var base Baseline
	if found, err := statefile.Load(basePath, &base); err != nil {
		fmt.Println(err)
		return 1
	} else if !found {
		base = Baseline{Sections: []string{}, Values: map[string]string{}}
	}
	changes := diffBaseline(base.Values, snapshot.Values, snapshot.Sections, cfg.IgnorePrefixes)
	base.Values = mergeBaseline(base.Values, snapshot.Values, snapshot.Sections)
	base.Sections = uniqueSorted(append(base.Sections, snapshot.Sections...))
	accepted := float64(time.Now().UnixNano()) / float64(time.Second)
	base.Accepted = &accepted
	if err := statefile.Save(basePath, base); err != nil {
		fmt.Println(err)
		return 1
	}
	text := formatChanges(changes, 200)
	if text == "" {
		text = "sin cambios"
	}
	fmt.Println(text)
	fmt.Printf("línea base actualizada (%d cambios aceptados)\n", len(changes))
	return 0
}

func Show(cfg Config) int {
	var base Baseline
	baseFound, baseErr := statefile.Load(filepath.Join(cfg.DataDir, "baseline.json"), &base)
	var snapshot Snapshot
	snapshotFound, snapshotErr := statefile.Load(filepath.Join(cfg.DataDir, "last_snapshot.json"), &snapshot)
	if baseErr != nil || snapshotErr != nil || !baseFound || !snapshotFound {
		fmt.Println("falta línea base o instantánea")
		return 1
	}
	text := formatChanges(diffBaseline(base.Values, snapshot.Values, snapshot.Sections, cfg.IgnorePrefixes), 500)
	if text == "" {
		text = "sin cambios"
	}
	fmt.Println(text)
	return 0
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func difference(values, excluded []string) []string {
	var out []string
	for _, value := range values {
		if !slices.Contains(excluded, value) {
			out = append(out, value)
		}
	}
	return out
}

func joinComma(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += value
	}
	return result
}
