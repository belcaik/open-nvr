// Package media integra go2rtc: genera su configuración y reenvía la
// señalización del navegador, que nunca habla con go2rtc directamente.
package media

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	NVRHost      string
	RTSPPort     int
	User         string
	Pass         string
	Channels     []int
	APIListen    string // solo loopback: la app web es la única cliente
	WebRTCListen string
	Candidate    string // IP:puerto que se anuncia al navegador
	VAAPI        bool
}

// StreamName: camN (MAIN), camN_sub (SUB) y sus variantes _h264
// transcodificadas para navegadores sin HEVC.
func StreamName(ch int, sub, h264 bool) string {
	name := fmt.Sprintf("cam%d", ch)
	if sub {
		name += "_sub"
	}
	if h264 {
		name += "_h264"
	}
	return name
}

// Render devuelve el YAML de go2rtc. Se escribe a mano para que la salida
// sea determinista y no haga falta una dependencia YAML.
func Render(c Config) string {
	var b strings.Builder
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	fmt.Fprintf(&b, "api:\n  listen: %s\n  origin: \"*\"\n", q(c.APIListen))
	// ffmpeg (transcodificación) lee la fuente del servidor RTSP interno de
	// go2rtc; solo escucha en loopback.
	b.WriteString("rtsp:\n  listen: \"127.0.0.1:8554\"\n")
	fmt.Fprintf(&b, "webrtc:\n  listen: %s\n  candidates:\n    - %s\n", q(c.WebRTCListen), q(c.Candidate))
	b.WriteString("log:\n  level: warn\nffmpeg:\n  hevc: \"-f hevc -i {input}\"\nstreams:\n")
	userinfo := url.UserPassword(c.User, c.Pass).String()
	hw := ""
	if c.VAAPI {
		hw = "#hardware=vaapi"
	}
	for _, ch := range c.Channels {
		for _, sub := range []bool{false, true} {
			quality := "MAIN"
			if sub {
				quality = "SUB"
			}
			src := fmt.Sprintf("rtsp://%s@%s:%d/live/%d/%s#transport=tcp", userinfo, c.NVRHost, c.RTSPPort, ch, quality)
			fmt.Fprintf(&b, "  %s: %s\n", StreamName(ch, sub, false), q(src))
		}
		for _, sub := range []bool{false, true} {
			src := fmt.Sprintf("ffmpeg:%s#video=h264%s#audio=opus", StreamName(ch, sub, false), hw)
			fmt.Fprintf(&b, "  %s: %s\n", StreamName(ch, sub, true), q(src))
		}
	}
	return b.String()
}

// WriteConfig escribe la configuración con modo 0600 (lleva credenciales).
func WriteConfig(path string, c Config) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(Render(c)), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
