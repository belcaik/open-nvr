package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // la imagen busybox no trae zoneinfo

	"github.com/belcaik/open-nvr/internal/auth"
	"github.com/belcaik/open-nvr/internal/camera"
	"github.com/belcaik/open-nvr/internal/config"
	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/media"
	"github.com/belcaik/open-nvr/internal/nvr"
	"github.com/belcaik/open-nvr/internal/web"
)

func init() {
	commands["web"] = runWeb
}

func runWeb([]string) int {
	if err := serveWeb(); err != nil {
		logx.Printf("web: %v", err)
		return 1
	}
	return 0
}

func serveWeb() error {
	host := config.Str("NVR_HOST", "192.168.0.227")
	user := config.Str("NVR_USER", "admin")
	pass := config.Secret("NVR_PASS")
	config.RegisterBasic(user, pass)
	if pass == "" {
		logx.Printf("aviso: sin NVR_PASS/NVR_PASS_FILE; la API y el vídeo fallarán con 401")
	}
	passHash := extractPHC(config.Secret("WEB_PASSWORD_HASH"))
	if passHash == "" {
		return errors.New("falta WEB_PASSWORD_HASH(_FILE); generar con `opennvr hash-password`")
	}
	key, err := base64.StdEncoding.DecodeString(config.Secret("WEB_SESSION_KEY"))
	if err != nil || len(key) < 32 {
		return errors.New("WEB_SESSION_KEY(_FILE) debe ser base64 de 32 bytes o más; generar con `opennvr hash-password --gen-key`")
	}
	var trusted []netip.Prefix
	for _, p := range config.List("WEB_TRUSTED_PROXIES", []string{"127.0.0.1/32", "192.168.0.8/32"}) {
		pr, err := netip.ParsePrefix(p)
		if err != nil {
			return fmt.Errorf("WEB_TRUSTED_PROXIES: %w", err)
		}
		trusted = append(trusted, pr)
	}
	channels := config.Ports("CAMERA_CHANNELS", []int{1, 2, 3, 4})
	apiListen := config.Str("GO2RTC_API", "127.0.0.1:1984")

	mcfg := media.Config{
		NVRHost: host, RTSPPort: config.Int("NVR_RTSP_PORT", 554), User: user, Pass: pass,
		Channels: channels, APIListen: apiListen, WebRTCListen: ":8555",
		Candidate: config.Str("GO2RTC_CANDIDATE", "192.168.0.8:8555"),
		VAAPI:     config.Bool("GO2RTC_VAAPI", true),
	}
	cfgPath := config.Str("GO2RTC_CONFIG", "/config/go2rtc.yaml")
	if err := media.WriteConfig(cfgPath, mcfg); err != nil {
		return fmt.Errorf("config de go2rtc: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	loc, err := time.LoadLocation(config.Str("NVR_TZ", "America/Santiago"))
	if err != nil {
		return fmt.Errorf("NVR_TZ: %w", err)
	}
	client := nvr.New("http://"+net.JoinHostPort(host, strconv.Itoa(config.Int("NVR_HTTP_PORT", 80))), user, pass).WithLocation(loc)
	guard := camera.NewGuard(client, 1500*time.Millisecond, time.Now)
	guardDone := make(chan struct{})
	go func() { guard.Run(ctx); close(guardDone) }()
	hub := camera.NewHub()
	go hub.Run(ctx, client.Events, channels)

	handler := web.NewServer(web.Deps{
		Cameras: &camera.Service{NVR: client, Guard: guard},
		Sessions: &auth.Sessions{Key: key, Idle: 7 * 24 * time.Hour, MaxAge: 30 * 24 * time.Hour,
			Refresh: time.Hour, Now: time.Now},
		Limiter:        auth.NewLimiter(5, 15*time.Minute, time.Now),
		PassHash:       passHash,
		User:           config.Str("WEB_USER", "admin"),
		Media:          media.NewProxy(apiListen, channels),
		Go2RTCHealth:   go2rtcHealth(apiListen),
		TrustedProxies: trusted,
		Channels:       channels,
		Config:         client,
		Recordings:     client,
		Events:         hub,
		Location:       loc,
		FailDelay:      500 * time.Millisecond,
	})
	listen := config.Str("WEB_LISTEN", "127.0.0.1:8095")
	srv := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logx.Printf("web: escuchando en %s, NVR %s, canales %v", listen, host, channels)

	select {
	case err := <-errc:
		stop()
		<-guardDone
		return err
	case <-ctx.Done():
	}
	logx.Printf("web: apagando")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	<-guardDone // Guard.Run para todos los movimientos al cancelar ctx
	return nil
}

func go2rtcHealth(apiListen string) func(context.Context) error {
	c := &http.Client{Timeout: 3 * time.Second}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+apiListen+"/api", nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("go2rtc HTTP %d", resp.StatusCode)
		}
		return nil
	}
}

// extractPHC devuelve el primer hash "$argon2id$..." del secreto. Tolera el
// texto que una TTY mezcla con la salida de hash-password (prompt, \r).
func extractPHC(s string) string {
	i := strings.Index(s, "$argon2id$")
	if i < 0 {
		return ""
	}
	s = s[i:]
	if j := strings.IndexAny(s, " \t\r\n"); j >= 0 {
		s = s[:j]
	}
	return s
}
