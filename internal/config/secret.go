package config

import (
	"encoding/base64"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	mu      sync.RWMutex
	secrets = map[string]bool{}
)

// Register añade un valor que nunca debe salir en claro.
func Register(v string) {
	if len(v) < 3 {
		return
	}
	mu.Lock()
	secrets[v] = true
	mu.Unlock()
}

// RegisterBasic registra también la cabecera Basic que se construye con ellos.
func RegisterBasic(user, pass string) {
	if pass != "" {
		Register(base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)))
	}
}

// Secret devuelve el contenido de NAME_FILE (si el archivo existe) o NAME.
func Secret(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if p := os.Getenv(name + "_FILE"); p != "" {
		// Como en Python: el archivo manda si existe; si no, se usa NAME.
		if b, err := os.ReadFile(p); err == nil {
			v = strings.TrimSpace(string(b))
		}
	}
	Register(v)
	return v
}

// Redact sustituye cualquier secreto registrado por ***.
func Redact(s string) string {
	mu.RLock()
	list := make([]string, 0, len(secrets))
	for k := range secrets {
		list = append(list, k)
	}
	mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, k := range list {
		s = strings.ReplaceAll(s, k, "***")
	}
	return s
}

// ResetSecrets solo para pruebas.
func ResetSecrets() {
	mu.Lock()
	secrets = map[string]bool{}
	mu.Unlock()
}
