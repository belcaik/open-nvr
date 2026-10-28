// Package logx escribe en stdout (lo recoge podman/journald), siempre redactado.
package logx

import (
	"fmt"
	"os"
	"time"

	"github.com/belcaik/open-nvr/internal/config"
)

func Printf(format string, args ...any) {
	msg := config.Redact(fmt.Sprintf(format, args...))
	fmt.Fprintf(os.Stdout, "%s %s\n", time.Now().Format("2006-01-02T15:04:05-0700"), msg)
}
