package main

import "testing"

func TestExtractPHC(t *testing.T) {
	h := "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"
	for in, want := range map[string]string{
		h:                               h,
		"Contraseña: \r\n" + h + "\r\n": h,
		"\n" + h + " ":                  h,
		"sin hash":                      "",
		"":                              "",
	} {
		if got := extractPHC(in); got != want {
			t.Fatalf("%q → %q", in, got)
		}
	}
}
