package nvrwatch

import (
	"strings"

	configpkg "github.com/belcaik/open-nvr/internal/config"
)

var (
	knownOpen     = []int{80, 554, 6060, 6066, 8885, 9000}
	defaultIgnore = []string{"status.", "camstatus.", "ntp.LastSuccessSyncNTPTime", "ntp.IsSynchronizeNTPNow", "ntp.SynchronizeNTPNowStatus"}
)

type Config struct {
	Host, RTSPPath, User, Password                string
	HTTPPort, RTSPPort                            int
	KnownPorts, ScanExtra                         []int
	ScanEnabled                                   bool
	CheckInterval, BaselineInterval, ScanInterval int
	FailThreshold, TimeDriftMax                   int
	ExpectedGateway                               string
	ExpectP2POffline                              bool
	P2POnlineStates, IgnorePrefixes               []string
	DataDir, NtfyURL, NtfyTopic, NtfyToken        string
	NotifyMinInterval, NotifyMaxPerHour           int
	BaselineRealert                               int
}

func defaultConfig() Config {
	return Config{Host: "192.168.0.227", HTTPPort: 80, RTSPPort: 554, RTSPPath: "/live/0/SUB", KnownPorts: append([]int(nil), knownOpen...), ScanEnabled: true, CheckInterval: 60, BaselineInterval: 900, ScanInterval: 21600, FailThreshold: 3, TimeDriftMax: 120, P2POnlineStates: []string{"internet-ready"}, IgnorePrefixes: append([]string(nil), defaultIgnore...), DataDir: "/data", NtfyTopic: "opennvr", NotifyMinInterval: 3600, NotifyMaxPerHour: 20, BaselineRealert: 86400}
}

// FromEnv carga la configuración y registra los secretos para redacción.
func FromEnv() Config {
	cfg := defaultConfig()
	cfg.Host = configpkg.Str("NVR_HOST", cfg.Host)
	cfg.HTTPPort = configpkg.Int("NVR_HTTP_PORT", cfg.HTTPPort)
	cfg.RTSPPort = configpkg.Int("NVR_RTSP_PORT", cfg.RTSPPort)
	cfg.RTSPPath = configpkg.Str("NVR_RTSP_PATH", cfg.RTSPPath)
	cfg.User = configpkg.Str("NVR_USER", "")
	cfg.Password = configpkg.Secret("NVR_PASS")
	configpkg.RegisterBasic(cfg.User, cfg.Password)
	cfg.KnownPorts = configpkg.Ports("NVR_KNOWN_TCP_PORTS", cfg.KnownPorts)
	cfg.ScanExtra = configpkg.Ports("SCAN_EXTRA_PORTS", nil)
	cfg.ScanEnabled = configpkg.Bool("SCAN_ENABLED", cfg.ScanEnabled)
	cfg.CheckInterval = configpkg.Int("CHECK_INTERVAL", cfg.CheckInterval)
	cfg.BaselineInterval = configpkg.Int("BASELINE_INTERVAL", cfg.BaselineInterval)
	cfg.ScanInterval = configpkg.Int("SCAN_INTERVAL", cfg.ScanInterval)
	cfg.FailThreshold = configpkg.Int("FAIL_THRESHOLD", cfg.FailThreshold)
	cfg.TimeDriftMax = configpkg.Int("TIME_DRIFT_MAX", cfg.TimeDriftMax)
	cfg.ExpectedGateway = configpkg.Str("EXPECTED_GATEWAY", "")
	cfg.ExpectP2POffline = configpkg.Bool("EXPECT_P2P_OFFLINE", false)
	cfg.P2POnlineStates = configpkg.List("P2P_ONLINE_STATES", cfg.P2POnlineStates)
	for index := range cfg.P2POnlineStates {
		cfg.P2POnlineStates[index] = strings.ToLower(cfg.P2POnlineStates[index])
	}
	cfg.IgnorePrefixes = configpkg.List("IGNORE_PREFIXES", cfg.IgnorePrefixes)
	cfg.DataDir = configpkg.Str("DATA_DIR", cfg.DataDir)
	cfg.NtfyURL = configpkg.Str("NTFY_URL", "")
	cfg.NtfyTopic = configpkg.Str("NTFY_TOPIC", cfg.NtfyTopic)
	cfg.NtfyToken = configpkg.Secret("NTFY_TOKEN")
	cfg.NotifyMinInterval = configpkg.Int("NOTIFY_MIN_INTERVAL", cfg.NotifyMinInterval)
	cfg.NotifyMaxPerHour = configpkg.Int("NOTIFY_MAX_PER_HOUR", cfg.NotifyMaxPerHour)
	cfg.BaselineRealert = configpkg.Int("BASELINE_REALERT", cfg.BaselineRealert)
	return cfg
}
