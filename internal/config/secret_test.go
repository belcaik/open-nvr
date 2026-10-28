package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecretFileWinsAndIsRedacted(t *testing.T) {
	ResetSecrets()
	p := filepath.Join(t.TempDir(), "s")
	os.WriteFile(p, []byte("archivo-secreto\n"), 0o600)
	t.Setenv("S_FILE", p)
	t.Setenv("S", "entorno-secreto")
	if got := Secret("S"); got != "archivo-secreto" {
		t.Fatalf("Secret = %q", got)
	}
	if got := Redact("x archivo-secreto y"); got != "x *** y" {
		t.Fatalf("Redact = %q", got)
	}
}

func TestSecretEnvFallbackAndShortIgnored(t *testing.T) {
	ResetSecrets()
	t.Setenv("S", "ab")
	if Secret("S") != "ab" || Redact("ab") != "ab" {
		t.Fatal("los secretos de menos de 3 caracteres no se redactan")
	}
}

func TestRegisterBasic(t *testing.T) {
	ResetSecrets()
	RegisterBasic("admin", "clave123")
	if got := Redact("Authorization: Basic YWRtaW46Y2xhdmUxMjM="); got != "Authorization: Basic ***" {
		t.Fatalf("Redact = %q", got)
	}
}

func TestSecretMissingFileFallsBackToEnv(t *testing.T) {
	ResetSecrets()
	t.Setenv("S_FILE", filepath.Join(t.TempDir(), "no-existe"))
	t.Setenv("S", "entorno-secreto")
	if got := Secret("S"); got != "entorno-secreto" {
		t.Fatalf("Secret = %q", got)
	}
}
