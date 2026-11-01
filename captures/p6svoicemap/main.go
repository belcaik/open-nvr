// Command p6svoicemap averigua qué cámara reproduce cada canal de voz P6S.
// Envía un tono corto por un canal de voz y, a la vez, graba el audio RTSP de
// cada cámara: la que capta el tono con su micrófono es la que lo reproduce.
//
//	source ~/.config/open-nvr/env && go run ./captures/p6svoicemap -voice 0,1 -listen 1,2
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s/voice"
	"github.com/belcaik/open-nvr/internal/p6sclient"
)

const sampleRate = 8000

func main() {
	voices := flag.String("voice", "0,1", "canales de voz P6S (base 0) a probar, en orden")
	listen := flag.String("listen", "1,2", "canales RTSP /live/N a escuchar")
	toneSec := flag.Float64("tone", 2, "segundos de tono por canal de voz")
	amp := flag.Float64("amp", 0.25, "amplitud del tono (0-1)")
	beeps := flag.Bool("beeps", false, "pitidos: el canal de voz i-ésimo emite i+1 pitidos de 0,3 s")
	leadSec := flag.Float64("lead", 5, "segundos de espera antes del primer canal")
	keep := flag.Bool("keep", false, "conservar las grabaciones RAW en TMPDIR")
	flag.Parse()
	host, user, pass := os.Getenv("NVR_HOST"), os.Getenv("NVR_USER"), os.Getenv("NVR_PASS")
	if host == "" || user == "" || pass == "" {
		fatal(fmt.Errorf("faltan NVR_HOST, NVR_USER o NVR_PASS"))
	}
	voiceChannels, listenChannels := ints(*voices), ints(*listen)
	freqs := make([]float64, len(voiceChannels))
	for i := range freqs {
		freqs[i] = 700 + 400*float64(i) // 700, 1100, 1500... Hz
	}
	const gap = 4 * time.Second
	lead := time.Duration(*leadSec * float64(time.Second))
	tone := time.Duration(*toneSec * float64(time.Second))
	if *beeps {
		tone = time.Duration(len(voiceChannels)) * 600 * time.Millisecond
	}
	total := lead + time.Duration(len(voiceChannels))*(tone+gap) + 2*time.Second

	recorders := make(map[int]*exec.Cmd)
	files := make(map[int]string)
	for _, ch := range listenChannels {
		files[ch] = fmt.Sprintf("%s/voicemap-live%d.raw", os.TempDir(), ch)
		src := fmt.Sprintf("rtsp://%s:%s@%s:554/live/%d/SUB", user, pass, host, ch)
		cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "quiet", "-y", "-rtsp_transport", "tcp", "-i", src,
			"-vn", "-ac", "1", "-ar", strconv.Itoa(sampleRate), "-f", "s16le", "-t", fmt.Sprint(total.Seconds()), files[ch])
		if err := cmd.Start(); err != nil {
			fatal(fmt.Errorf("ffmpeg canal %d: %w", ch, err))
		}
		recorders[ch] = cmd
	}
	start := time.Now()
	// El RTSP tarda en arrancar: el instante 0 de cada archivo es su primer byte.
	fileStart := make(map[int]time.Duration)
	for time.Since(start) < lead {
		for ch, path := range files {
			if _, ok := fileStart[ch]; ok {
				continue
			}
			if st, err := os.Stat(path); err == nil && st.Size() > 0 {
				fileStart[ch] = time.Since(start)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, ch := range listenChannels {
		fmt.Printf("live/%d: primer byte en t=%.2f s\n", ch, fileStart[ch].Seconds())
	}

	ext, err := voice.CapturedAudioExtension()
	if err != nil {
		fatal(err)
	}
	offsets := make([]time.Duration, len(voiceChannels))
	for i, vc := range voiceChannels {
		offsets[i] = time.Since(start)
		fmt.Printf("voz %d: tono %.0f Hz desde t=%.2f s\n", vc, freqs[i], offsets[i].Seconds())
		count := 0
		if *beeps {
			count = i + 1
		}
		if err := sendTone(host, user, pass, ext, vc, freqs[i], *amp, tone, count); err != nil {
			fmt.Printf("voz %d: error %v\n", vc, err)
		}
		time.Sleep(gap)
	}
	for ch, cmd := range recorders {
		if err := cmd.Wait(); err != nil {
			fmt.Printf("ffmpeg canal %d terminó con error (sin detalles para no mostrar la URL)\n", ch)
		}
	}

	fmt.Printf("\npotencia del tono por cámara (dB sobre el ruido previo; >10 dB = la cámara lo oyó)\n")
	fmt.Printf("%-12s", "voz\\RTSP")
	for _, ch := range listenChannels {
		fmt.Printf("  live/%d", ch)
	}
	fmt.Println()
	for i, vc := range voiceChannels {
		fmt.Printf("voz %-2d %4.0fHz", vc, freqs[i])
		for _, ch := range listenChannels {
			samples := readS16(files[ch])
			base := goertzel(window(samples, 500*time.Millisecond, 2*time.Second), freqs[i])
			// La latencia RTSP desplaza el tono; se busca la mejor ventana.
			best := 0.0
			for d := -2500 * time.Millisecond; d <= 2500*time.Millisecond; d += 250 * time.Millisecond {
				p := goertzel(window(samples, offsets[i]+d, offsets[i]+d+tone/2), freqs[i])
				best = math.Max(best, p)
			}
			fmt.Printf("  %6.1f", 10*math.Log10((best+1e-9)/(base+1e-9)))
		}
		fmt.Println()
	}
	for _, ch := range listenChannels {
		fmt.Printf("live/%d: %.1f s de audio grabado\n", ch, float64(len(readS16(files[ch])))/sampleRate)
	}
	for _, f := range files {
		if *keep {
			continue
		}
		_ = os.Remove(f)
	}
}

// sendTone emite un tono continuo o, con beeps>0, ese número de pitidos de
// 0,3 s separados por 0,3 s de silencio dentro de d.
func sendTone(host, user, pass string, ext voice.AudioExtension, channel int, freq, amp float64, d time.Duration, beeps int) error {
	ctx, cancel := context.WithTimeout(context.Background(), d+15*time.Second)
	defer cancel()
	command, media, err := p6sclient.Connect(ctx, p6sclient.Config{Host: host, Username: user, Password: pass})
	if err != nil {
		return err
	}
	s, err := voice.Open(ctx, command, media, voice.Config{Channel: channel, Extension: ext})
	if err != nil {
		_ = command.Close()
		_ = media.Close()
		return err
	}
	defer s.Close()
	n := 0
	for frames := int(d / (40 * time.Millisecond)); frames > 0; frames-- {
		pcm := make([]byte, voice.PCM16FrameBytes)
		for i := 0; i < len(pcm)/2; i++ {
			a := amp
			if beeps > 0 {
				t := float64(n) / sampleRate
				if beep := int(t / 0.6); beep >= beeps || t-float64(beep)*0.6 >= 0.3 {
					a = 0
				}
			}
			v := int16(a * 32767 * math.Sin(2*math.Pi*freq*float64(n)/sampleRate))
			binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
			n++
		}
		pcmu, err := voice.EncodePCMU(pcm)
		if err != nil {
			return err
		}
		if err := s.Send(ctx, pcmu); err != nil {
			return err
		}
	}
	return nil
}

func goertzel(x []int16, freq float64) float64 {
	if len(x) == 0 {
		return 0
	}
	k := 2 * math.Cos(2*math.Pi*freq/sampleRate)
	var s1, s2 float64
	for _, v := range x {
		s := float64(v) + k*s1 - s2
		s2, s1 = s1, s
	}
	return (s1*s1 + s2*s2 - k*s1*s2) / float64(len(x))
}

func window(x []int16, from, to time.Duration) []int16 {
	a, b := int(from.Seconds()*sampleRate), int(to.Seconds()*sampleRate)
	if a < 0 {
		a = 0
	}
	if b > len(x) {
		b = len(x)
	}
	if a >= b {
		return nil
	}
	return x[a:b]
}

func readS16(path string) []int16 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make([]int16, len(raw)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	return out
}

func ints(s string) []int {
	var out []int
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			fatal(fmt.Errorf("número inválido %q", f))
		}
		out = append(out, n)
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
