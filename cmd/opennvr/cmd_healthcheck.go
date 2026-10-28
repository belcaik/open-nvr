package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/belcaik/open-nvr/internal/health"
)

func init() {
	commands["healthcheck"] = runHealthcheck
}

// runHealthcheck: `--file <latido> --max-age <dur>` o `--url <url>`.
func runHealthcheck(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	file := fs.String("file", "", "archivo de latido")
	maxAge := fs.Duration("max-age", 3*time.Minute, "antigüedad máxima del latido")
	url := fs.String("url", "", "URL que debe responder 200")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var err error
	switch {
	case *file != "":
		err = health.Check(*file, *maxAge, time.Now())
	case *url != "":
		err = health.CheckURL(*url)
	default:
		fmt.Fprintln(os.Stderr, "uso: opennvr healthcheck --file F [--max-age D] | --url U")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	return 0
}
