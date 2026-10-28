package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const golden = `api:
  listen: "127.0.0.1:1984"
  origin: "*"
rtsp:
  listen: "127.0.0.1:8554"
webrtc:
  listen: ":8555"
  candidates:
    - "192.168.0.8:8555"
log:
  level: warn
streams:
  cam1: "rtsp://admin:p%40ss%3Aw%2Frd@192.168.0.227:554/live/1/MAIN#transport=tcp"
  cam1_sub: "rtsp://admin:p%40ss%3Aw%2Frd@192.168.0.227:554/live/1/SUB#transport=tcp"
  cam1_h264: "ffmpeg:cam1#video=h264#hardware=vaapi#audio=opus"
  cam1_sub_h264: "ffmpeg:cam1_sub#video=h264#hardware=vaapi#audio=opus"
`

func cfg() Config {
	return Config{NVRHost: "192.168.0.227", RTSPPort: 554, User: "admin", Pass: "p@ss:w/rd",
		Channels: []int{1}, APIListen: "127.0.0.1:1984", WebRTCListen: ":8555",
		Candidate: "192.168.0.8:8555", VAAPI: true}
}

func TestRenderGolden(t *testing.T) {
	if got := Render(cfg()); got != golden {
		t.Fatalf("YAML distinto:\n%s", got)
	}
}

func TestRenderNoVAAPI(t *testing.T) {
	c := cfg()
	c.VAAPI = false
	got := Render(c)
	if strings.Contains(got, "vaapi") || !strings.Contains(got, `cam1_h264: "ffmpeg:cam1#video=h264#audio=opus"`) {
		t.Fatal(got)
	}
}

func TestStreamName(t *testing.T) {
	cases := map[string]string{
		StreamName(2, false, false): "cam2", StreamName(2, true, false): "cam2_sub",
		StreamName(2, false, true): "cam2_h264", StreamName(2, true, true): "cam2_sub_h264",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("%s != %s", got, want)
		}
	}
}

func TestWriteConfigMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go2rtc.yaml")
	if err := WriteConfig(p, cfg()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", err, st.Mode())
	}
}
