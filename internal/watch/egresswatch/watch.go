package egresswatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/config"
	"github.com/belcaik/open-nvr/internal/health"
	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/notify"
	"github.com/belcaik/open-nvr/internal/statefile"
)

type Config struct {
	Aggregate         AggregateConfig
	UlogFiles         []string
	DNSFile           string
	DataDir           string
	PollSeconds       float64
	LogMaxBytes       int64
	NTFYURL           string
	NTFYTopic         string
	NTFYToken         string
	NotifyMinInterval float64
	NotifyMaxPerHour  int
}

func defaultAggregateConfig() AggregateConfig {
	return AggregateConfig{
		NVRIP:         "192.168.0.227",
		WindowSeconds: 300,
		SpikeFactor:   5,
		SpikeMinBytes: 1_000_000,
		EWMAAlpha:     .2,
		SummaryHour:   8,
		SilenceHours:  6,
	}
}

// LoadConfig conserva los nombres de entorno y valores por defecto de Python.
func LoadConfig() Config {
	aggregate := defaultAggregateConfig()
	aggregate.NVRIP = config.Str("NVR_HOST", aggregate.NVRIP)
	aggregate.WindowSeconds = config.Int("WINDOW_SECONDS", aggregate.WindowSeconds)
	aggregate.SpikeFactor = config.Float("SPIKE_FACTOR", aggregate.SpikeFactor)
	aggregate.SpikeMinBytes = config.Int("SPIKE_MIN_BYTES", aggregate.SpikeMinBytes)
	aggregate.SummaryHour = config.Int("SUMMARY_HOUR", aggregate.SummaryHour)
	aggregate.SilenceHours = config.Float("SILENCE_ALERT_HOURS", aggregate.SilenceHours)
	return Config{
		Aggregate:         aggregate,
		UlogFiles:         config.List("ULOG_FILES", []string{"/var/log/ulogd/nvr-fwd.json", "/var/log/ulogd/nvr-drop.json"}),
		DNSFile:           config.Str("DNSMASQ_LOG", "/var/log/dnsmasq/queries.log"),
		DataDir:           config.Str("DATA_DIR", "/data"),
		PollSeconds:       config.Float("POLL_SECONDS", 5),
		LogMaxBytes:       int64(config.Int("LOG_MAX_BYTES", 50_000_000)),
		NTFYURL:           config.Str("NTFY_URL", ""),
		NTFYTopic:         config.Str("NTFY_TOPIC", "opennvr"),
		NTFYToken:         config.Secret("NTFY_TOKEN"),
		NotifyMinInterval: float64(config.Int("NOTIFY_MIN_INTERVAL", 3600)),
		NotifyMaxPerHour:  config.Int("NOTIFY_MAX_PER_HOUR", 20),
	}
}

type WatchState struct {
	Aggregate AggregateState        `json:"agg"`
	Throttle  notify.ThrottleState  `json:"throttle"`
	Tails     map[string]*TailState `json:"tails"`
}

type Watch struct {
	Config     Config
	State      *WatchState
	Aggregator *Aggregator
	Notifier   *notify.Notifier

	clock     func() float64
	today     func() time.Time
	statePath string
	ulogTails []*FileTail
	dnsTail   *FileTail
	lastSave  float64
}

func New(cfg Config, clock func() float64, today func() time.Time) (*Watch, error) {
	if clock == nil {
		clock = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	if today == nil {
		today = time.Now
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	statePath := filepath.Join(cfg.DataDir, "state.json")
	state := &WatchState{}
	if _, err := statefile.Load(statePath, state); err != nil {
		return nil, err
	}
	if state.Tails == nil {
		state.Tails = map[string]*TailState{}
	}
	now := clock()
	aggregator := NewAggregator(&state.Aggregate, cfg.Aggregate, now, today().Format("2006-01-02"))
	throttle := notify.NewThrottle(&state.Throttle, cfg.NotifyMinInterval, cfg.NotifyMaxPerHour)
	watch := &Watch{Config: cfg, State: state, Aggregator: aggregator, clock: clock, today: today, statePath: statePath}
	watch.Notifier = &notify.Notifier{URL: cfg.NTFYURL, Topic: cfg.NTFYTopic, Token: cfg.NTFYToken, Prefix: "NVR tráfico: ", Throttle: throttle}
	for _, path := range cfg.UlogFiles {
		if state.Tails[path] == nil {
			state.Tails[path] = &TailState{}
		}
		watch.ulogTails = append(watch.ulogTails, NewFileTail(path, state.Tails[path]))
	}
	if state.Tails[cfg.DNSFile] == nil {
		state.Tails[cfg.DNSFile] = &TailState{}
	}
	watch.dnsTail = NewFileTail(cfg.DNSFile, state.Tails[cfg.DNSFile])
	return watch, nil
}

func (w *Watch) Poll() error {
	now := w.clock()
	var alerts []Alert
	lines, err := w.dnsTail.ReadLines(0)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if event, ok := ParseDNSMasqLine(line); ok {
			alerts = append(alerts, w.Aggregator.DNS(event, now)...)
		}
	}
	for _, tail := range w.ulogTails {
		lines, err := tail.ReadLines(0)
		if err != nil {
			return err
		}
		for _, line := range lines {
			if flow, ok := ParseUlogLine(line); ok {
				alerts = append(alerts, w.Aggregator.Flow(flow, now)...)
			}
		}
	}
	alerts = append(alerts, w.Aggregator.Tick(now, w.today())...)
	for _, alert := range alerts {
		w.Notifier.Notify(alert.Key, alert.Title, alert.Message, alert.Priority, alert.Tags, alert.Interval, now)
	}
	allTails := append(append([]*FileTail(nil), w.ulogTails...), w.dnsTail)
	for _, tail := range allTails {
		truncated, err := tail.TruncateIfLarger(w.Config.LogMaxBytes)
		if err != nil {
			logx.Printf("no se pudo vaciar %s: %T", tail.Path, err)
		} else if truncated {
			logx.Printf("%s vaciado (superaba LOG_MAX_BYTES)", tail.Path)
		}
	}
	if len(alerts) > 0 || now-w.lastSave >= 30 {
		w.Notifier.Throttle.Prune(now, 7*86400)
		if err := statefile.Save(w.statePath, w.State); err != nil {
			return err
		}
		w.lastSave = now
	}
	return nil
}

// RunForever ejecuta sondeos hasta cancelar el contexto y actualiza el latido.
func (w *Watch) RunForever(ctx context.Context) {
	files := append(append([]string(nil), w.Config.UlogFiles...), w.Config.DNSFile)
	logx.Printf("egress-watch: NVR %s, archivos %s", w.Config.Aggregate.NVRIP, strings.Join(files, ", "))
	for {
		if err := w.Poll(); err != nil {
			logx.Printf("error en el ciclo: %T: %v", err, err)
		}
		if err := health.Beat("/tmp/heartbeat"); err != nil {
			logx.Printf("error escribiendo latido: %T: %v", err, err)
		}
		timer := time.NewTimer(time.Duration(w.Config.PollSeconds * float64(time.Second)))
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

func (w *Watch) Summary() string {
	return w.Aggregator.FormatSummary(w.State.Aggregate.Daily)
}
