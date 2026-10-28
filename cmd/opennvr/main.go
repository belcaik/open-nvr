package main

import (
	"fmt"
	"os"
)

var version = "dev"

// commands lo rellenan los archivos cmd_*.go de cada subcomando.
var commands = map[string]func(args []string) int{
	"version": func([]string) int { fmt.Println(version); return 0 },
}

func main() {
	if len(os.Args) < 2 || commands[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "uso: opennvr <web|nvr-watch|egress-watch|healthcheck|hash-password|version> [args]")
		os.Exit(2)
	}
	os.Exit(commands[os.Args[1]](os.Args[2:]))
}
