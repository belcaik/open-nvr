// Package config lee la configuración del entorno y los secretos de archivo.
package config

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

func Str(name, def string) string {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func Int(name string, def int) int {
	if v := Str(name, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func Float(name string, def float64) float64 {
	if v := Str(name, ""); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func Bool(name string, def bool) bool {
	switch strings.ToLower(Str(name, "")) {
	case "":
		return def
	case "1", "true", "yes", "si", "sí", "on":
		return true
	}
	return false
}

func List(name string, def []string) []string {
	v := Str(name, "")
	if v == "" {
		return append([]string(nil), def...)
	}
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func Ports(name string, def []int) []int {
	defs := make([]string, len(def))
	for i, p := range def {
		defs[i] = strconv.Itoa(p)
	}
	seen := map[int]bool{}
	var out []int
	for _, s := range List(name, defs) {
		if p, err := strconv.Atoi(s); err == nil && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}
