package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Go2RTCStreams implementa sólo el alta, consulta y baja documentados en
// /api/streams. La API debe escuchar en loopback; no se envían credenciales.
type Go2RTCStreams struct {
	base   string
	client *http.Client
}

func NewGo2RTCStreams(apiListen string, client *http.Client) (*Go2RTCStreams, error) {
	if strings.ContainsAny(apiListen, "/?#@") || apiListen == "" {
		return nil, errors.New("media: dirección de go2rtc no válida")
	}
	addrPort, err := netip.ParseAddrPort(apiListen)
	if err != nil || !addrPort.Addr().Unmap().IsLoopback() {
		return nil, errors.New("media: la API de go2rtc debe usar loopback")
	}
	if addrPort.Port() == 0 {
		return nil, errors.New("media: puerto de go2rtc no válido")
	}
	base, err := url.Parse("http://" + apiListen)
	if err != nil || base.Host == "" {
		return nil, errors.New("media: dirección de go2rtc no válida")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Go2RTCStreams{base: strings.TrimRight(base.String(), "/"), client: client}, nil
}

func (c *Go2RTCStreams) Add(ctx context.Context, name, source string) error {
	if err := validateTemporary(name, source); err != nil {
		return err
	}
	status, err := c.request(ctx, http.MethodPut, url.Values{"name": {name}, "src": {source}})
	if err == nil && status >= 200 && status < 300 {
		return nil
	}
	// v1.9.15 crea el stream antes de intentar persistirlo. Con la config
	// montada read-only puede responder 400 aunque el alta en memoria exista.
	verifyStatus, verifyErr := c.request(ctx, http.MethodGet, url.Values{"src": {name}})
	if verifyErr == nil && verifyStatus == http.StatusOK {
		return nil
	}
	if err != nil {
		return fmt.Errorf("media: alta go2rtc: %w", err)
	}
	return fmt.Errorf("media: alta go2rtc HTTP %d", status)
}

func (c *Go2RTCStreams) Delete(ctx context.Context, name string) error {
	if !temporaryName(name) {
		return errors.New("media: nombre temporal no válido")
	}
	status, err := c.request(ctx, http.MethodDelete, url.Values{"src": {name}})
	if err == nil && status >= 200 && status < 300 {
		return nil
	}
	// DELETE también elimina del mapa antes de intentar escribir la config.
	verifyStatus, verifyErr := c.request(ctx, http.MethodGet, url.Values{"src": {name}})
	if verifyErr == nil && verifyStatus == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("media: baja go2rtc: %w", err)
	}
	return fmt.Errorf("media: baja go2rtc HTTP %d", status)
}

func validateTemporary(name, source string) error {
	if !temporaryName(name) {
		return errors.New("media: nombre temporal no válido")
	}
	raw, ok := strings.CutPrefix(source, "ffmpeg:")
	if !ok {
		return errors.New("media: fuente temporal no válida")
	}
	rawURL, fragment, ok := strings.Cut(raw, "#")
	if !ok || fragment != "input=hevc#video=h264" {
		return errors.New("media: fuente temporal no válida")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("media: fuente temporal no válida")
	}
	return nil
}

func temporaryName(name string) bool {
	if !strings.HasPrefix(name, "playback_") || len(name) != len("playback_")+32 {
		return false
	}
	for _, r := range name[len("playback_"):] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func (c *Go2RTCStreams) request(ctx context.Context, method string, values url.Values) (int, error) {
	if ctx == nil {
		return 0, errors.New("contexto nil")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/streams?"+values.Encode(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, nil
}
