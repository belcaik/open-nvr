// Package logx escribe en stdout (lo recoge podman/journald), siempre redactado.
package logx

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/belcaik/open-nvr/internal/config"
)

// Out es el destino del registro; los tests lo sustituyen.
var Out io.Writer = os.Stdout

func Printf(format string, args ...any) {
	msg := config.Redact(fmt.Sprintf(format, args...))
	fmt.Fprintf(Out, "%s %s\n", time.Now().Format("2006-01-02T15:04:05-0700"), msg)
}
