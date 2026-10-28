package main

import "testing"

func TestNVRWatchCommandIsRegistered(t *testing.T) {
	command := commands["nvr-watch"]
	if command == nil {
		t.Fatal("nvr-watch no está registrado")
	}
	if code := command([]string{"desconocido"}); code != 2 {
		t.Fatalf("comando desconocido = %d", code)
	}
}
