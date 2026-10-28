package nvrwatch

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

var labels = map[string]string{
	"http:api":    "API HTTP (/System/DeviceInfo)",
	"rtsp":        "RTSP DESCRIBE",
	"time":        "hora del NVR",
	"gateway":     "puerta de enlace del NVR",
	"p2p_offline": "P2P aislado de Internet",
}

func label(name string) string {
	if value, ok := labels[name]; ok {
		return value
	}
	if strings.HasPrefix(name, "tcp:") {
		return "puerto " + strings.TrimPrefix(name, "tcp:") + "/tcp"
	}
	if strings.HasPrefix(name, "camera:") {
		return "cámara del canal " + strings.TrimPrefix(name, "camera:")
	}
	return name
}

func timeCheck(drift *float64, maxDrift int) *CheckResult {
	if drift == nil {
		return nil
	}
	return &CheckResult{Name: "time", OK: abs(*drift) <= float64(maxDrift), Detail: fmt.Sprintf("deriva %+.0f s (máx. %d s)", *drift, maxDrift)}
}

func gatewayCheck(values map[string]string, expected string) *CheckResult {
	if expected == "" {
		return nil
	}
	gateway := firstValue(values, ".Gateway.IPAddress")
	if gateway == "" {
		gateway = firstValue(values, ".Gateway")
	}
	if gateway == "" {
		return nil
	}
	return &CheckResult{Name: "gateway", OK: gateway == expected, Detail: fmt.Sprintf("puerta de enlace %s (esperada %s)", gateway, expected)}
}

func p2pCheck(values map[string]string, enabled bool, onlineStates []string) *CheckResult {
	if !enabled {
		return nil
	}
	var hits []string
	found := false
	for key, value := range values {
		if !strings.HasPrefix(key, "p2p.") {
			continue
		}
		found = true
		if slices.Contains(onlineStates, strings.ToLower(strings.TrimSpace(value))) {
			hits = append(hits, key+"="+value)
		}
	}
	if !found {
		return nil
	}
	slices.Sort(hits)
	if len(hits) > 0 {
		return &CheckResult{Name: "p2p_offline", Detail: "P2P conectado: " + strings.Join(hits, ", ")}
	}
	return &CheckResult{Name: "p2p_offline", OK: true, Detail: "P2P sin conexión a la nube"}
}

var (
	camStatusPattern = regexp.MustCompile(`^camstatus\.([^.]+)\.LoginStatus$`)
	cameraPattern    = regexp.MustCompile(`^camera\.([^.]+)\.`)
)

func cameraChecks(values map[string]string) []CheckResult {
	registered := map[string]bool{}
	for key := range values {
		if match := cameraPattern.FindStringSubmatch(key); match != nil {
			registered[match[1]] = true
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var results []CheckResult
	for _, key := range keys {
		match := camStatusPattern.FindStringSubmatch(key)
		if match == nil || !registered[match[1]] {
			continue
		}
		status := values[key]
		normalized := strings.ToLower(strings.TrimSpace(status))
		results = append(results, CheckResult{Name: "camera:" + match[1], OK: normalized == "connect success" || normalized == "weak password", Detail: fmt.Sprintf("LoginStatus='%s'", status)})
	}
	return results
}

func behaviourChecks(values map[string]string, drift *float64, cfg Config) []CheckResult {
	var results []CheckResult
	for _, check := range []*CheckResult{timeCheck(drift, cfg.TimeDriftMax), gatewayCheck(values, cfg.ExpectedGateway), p2pCheck(values, cfg.ExpectP2POffline, cfg.P2POnlineStates)} {
		if check != nil {
			results = append(results, *check)
		}
	}
	return append(results, cameraChecks(values)...)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
