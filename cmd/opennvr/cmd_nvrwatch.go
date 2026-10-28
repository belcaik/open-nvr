package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/watch/nvrwatch"
)

func init() {
	commands["nvr-watch"] = runNVRWatch
}

func runNVRWatch(args []string) int {
	command := "run"
	if len(args) > 0 {
		command = args[0]
	}
	if command != "run" && command != "once" && command != "accept" && command != "show" {
		fmt.Fprintln(os.Stderr, "uso: opennvr nvr-watch [run|once|accept|show]")
		return 2
	}
	cfg := nvrwatch.FromEnv()
	if cfg.Password == "" {
		logx.Printf("aviso: sin NVR_PASS/NVR_PASS_FILE; la API y el RTSP fallarán con 401")
	}
	switch command {
	case "accept":
		return nvrwatch.Accept(cfg)
	case "show":
		return nvrwatch.Show(cfg)
	}
	clock := func() float64 { return float64(time.Now().UnixNano()) / float64(time.Second) }
	app, err := nvrwatch.NewApp(cfg, nil, clock, time.Sleep)
	if err != nil {
		logx.Printf("no se pudo iniciar nvr-watch: %v", err)
		return 1
	}
	if command == "once" {
		if err := app.Once(clock()); err != nil {
			logx.Printf("error en el ciclo: %v", err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	app.RunForever(ctx)
	return 0
}
