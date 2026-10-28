package nvrwatch

import (
	"strconv"
	"strings"
	"testing"
)

var baselineValues = map[string]string{
	"device.SoftWareVersion":        "V1.0",
	"camera.1.IP":                   "192.0.2.14",
	"camera.1.MAC":                  "00:11",
	"port.80":                       "open",
	"port.554":                      "open",
	"status.MainStreamVideoBitrate": "2048",
}

func cloneValues(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func TestBaselineNoChanges(t *testing.T) {
	if got := diffBaseline(baselineValues, cloneValues(baselineValues), []string{"device", "camera", "port"}, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestBaselineAddedRemovedChanged(t *testing.T) {
	current := cloneValues(baselineValues)
	current["device.SoftWareVersion"] = "V2.0"
	delete(current, "port.554")
	current["port.23"] = "open"
	got := diffBaseline(baselineValues, current, []string{"device", "camera", "port"}, nil)
	if len(got) != 3 || got[0].Kind != Changed || got[0].Key != "device.SoftWareVersion" || got[1].Kind != Added || got[1].Key != "port.23" || got[2].Kind != Removed || got[2].Key != "port.554" {
		t.Fatalf("cambios = %#v", got)
	}
	if got[0].Old != "V1.0" || got[0].New != "V2.0" {
		t.Fatal(got[0])
	}
	if fingerprint(got) != "77cf9b4612430438" {
		t.Fatalf("huella incompatible con Python = %s", fingerprint(got))
	}
	wantText := "~ device.SoftWareVersion: 'V1.0' -> 'V2.0'\n+ port.23 = 'open'\n- port.554 (era 'open')"
	if text := formatChanges(got, 25); text != wantText {
		t.Fatalf("formato incompatible:\n%s", text)
	}
}

func TestBaselineMissingSectionDoesNotReportRemovals(t *testing.T) {
	current := cloneValues(baselineValues)
	delete(current, "camera.1.IP")
	delete(current, "camera.1.MAC")
	if got := diffBaseline(baselineValues, current, []string{"device", "port"}, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestBaselineIgnorePrefixes(t *testing.T) {
	current := cloneValues(baselineValues)
	current["status.MainStreamVideoBitrate"] = "10"
	if got := diffBaseline(baselineValues, current, []string{"status"}, []string{"status."}); len(got) != 0 {
		t.Fatal(got)
	}
	if got := diffBaseline(baselineValues, current, []string{"status"}, nil); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestBaselineFingerprintStableAndOrderIndependent(t *testing.T) {
	current := cloneValues(baselineValues)
	current["port.23"] = "open"
	current["device.SoftWareVersion"] = "V2"
	changes := diffBaseline(baselineValues, current, []string{"device", "port"}, nil)
	reversed := append([]Change(nil), changes...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	if fingerprint(changes) != fingerprint(reversed) {
		t.Fatal("la huella depende del orden")
	}
	other := cloneValues(baselineValues)
	other["port.22"] = "open"
	if fingerprint(changes) == fingerprint(diffBaseline(baselineValues, other, []string{"port"}, nil)) {
		t.Fatal("huellas distintas coinciden")
	}
}

func TestBaselineMergeReplacesOnlyGivenSections(t *testing.T) {
	merged := mergeBaseline(baselineValues, map[string]string{"port.80": "open", "camera.2.IP": "192.0.2.15"}, []string{"port"})
	if _, ok := merged["port.554"]; ok || merged["camera.1.IP"] != "192.0.2.14" {
		t.Fatal(merged)
	}
	if _, ok := merged["camera.2.IP"]; ok {
		t.Fatal(merged)
	}
}

func TestBaselineFormatChangesLimit(t *testing.T) {
	current := map[string]string{}
	for port := 0; port < 100; port++ {
		current["port."+strconv.Itoa(port)] = "open"
	}
	if text := formatChanges(diffBaseline(nil, current, []string{"port"}, nil), 5); !strings.Contains(text, "95 cambios más") {
		t.Fatal(text)
	}
}
