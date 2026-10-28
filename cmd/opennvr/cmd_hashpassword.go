package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/belcaik/open-nvr/internal/auth"
)

func init() {
	commands["hash-password"] = runHashPassword
}

// runHashPassword lee la contraseña de stdin (sin eco en una TTY) y muestra
// su hash argon2id. Con --gen-key muestra una clave de sesión aleatoria.
func runHashPassword(args []string) int {
	if len(args) == 1 && args[0] == "--gen-key" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println(base64.StdEncoding.EncodeToString(key))
		return 0
	}
	var pw string
	if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Contraseña: ")
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		pw = string(b)
	} else {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "la contraseña debe tener al menos 8 caracteres")
		return 1
	}
	h, err := auth.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(h)
	return 0
}
