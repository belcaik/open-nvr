package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/belcaik/open-nvr/internal/watch/egresswatch"
)

func init() {
	commands["egress-watch"] = runEgressWatch
}

func runEgressWatch(args []string) int {
	command := "run"
	if len(args) > 0 {
		command = args[0]
	}
	if len(args) > 1 || (command != "run" && command != "summary") {
		fmt.Fprintln(os.Stderr, "uso: opennvr egress-watch [run|summary]")
		return 2
	}
	watch, err := egresswatch.New(egresswatch.LoadConfig(), nil, time.Now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "egress-watch:", err)
		return 1
	}
	if command == "summary" {
		fmt.Println(watch.Summary())
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	watch.RunForever(ctx)
	return 0
}
