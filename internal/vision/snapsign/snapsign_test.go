package snapsign

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSignerKnownVectorAndRoundTrip(t *testing.T) {
	// RFC 4231 caso de prueba 2.
	s := Signer{Key: []byte("Jefe")}
	const msg = "what do ya want for nothing?"
	const want = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
	got := s.Sign(msg)
	if got != want {
		t.Fatalf("Sign = %q, quiero %q", got, want)
	}
	if !s.Verify(msg, got) {
		t.Fatal("Verify rechazó una firma válida")
	}

	rel := "snapshots/2026-11-04/1-100-enter.jpg"
	sig := s.Sign(rel)
	if len(sig) != 64 || !s.Verify(rel, sig) {
		t.Fatalf("firma de %q inválida: %q", rel, sig)
	}
	if s.Verify("snapshots/2026-11-04/otro.jpg", sig) {
		t.Fatal("Verify aceptó firma de otra ruta")
	}
}

func TestSignerVerifyRejectsMalformedAndEmptyKey(t *testing.T) {
	s := Signer{Key: bytes.Repeat([]byte{0xab}, 32)}
	rel := "snapshots/2026-11-04/abc.jpg"
	valid := s.Sign(rel)

	tampered := valid[:len(valid)-1] + "0"
	if valid[len(valid)-1] == '0' {
		tampered = valid[:len(valid)-1] + "1"
	}
	cases := map[string]string{
		"vacía":      "",
		"manipulada": tampered,
		"truncada":   valid[:63],
		"larga":      valid + "00",
		"mayúsculas": strings.ToUpper(valid),
		"no-hex":     strings.Repeat("z", 64),
	}
	for name, sig := range cases {
		if s.Verify(rel, sig) {
			t.Fatalf("Verify aceptó firma %s: %q", name, sig)
		}
	}

	emptyMac := hmac.New(sha256.New, nil)
	emptyMac.Write([]byte(rel))
	emptySig := hex.EncodeToString(emptyMac.Sum(nil))
	for _, empty := range []Signer{{}, {Key: []byte{}}} {
		if got := empty.Sign(rel); got != "" {
			t.Fatalf("Sign con clave vacía = %q, quiero cadena vacía", got)
		}
		if empty.Verify(rel, emptySig) || empty.Verify(rel, valid) || empty.Verify(rel, "") {
			t.Fatal("Verify con clave vacía devolvió true")
		}
	}
}

func TestLoadOrCreateKeyCreatesAndReuses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "vision")
	path := filepath.Join(dir, "snapshot-url.key")

	key1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey (crear): %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("len(key1) = %d, quiero 32", len(key1))
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o750 {
		t.Fatalf("permisos de directorio = %04o, quiero 0750", perm)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permisos de fichero = %04o, quiero 0600", perm)
	}

	key2, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey (reutilizar): %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Fatal("LoadOrCreateKey no reutilizó la clave existente")
	}
}

func TestLoadOrCreateKeyRejectsWrongSizeAndOpenPermissions(t *testing.T) {
	dir := t.TempDir()

	for _, size := range []int{0, 16, 31, 33, 64} {
		p := filepath.Join(dir, "size.key")
		if err := os.WriteFile(p, bytes.Repeat([]byte{'k'}, size), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateKey(p); err == nil {
			t.Fatalf("LoadOrCreateKey aceptó tamaño %d", size)
		}
		_ = os.Remove(p)
	}

	for _, perm := range []os.FileMode{0o644, 0o640, 0o604, 0o700, 0o666} {
		p := filepath.Join(dir, "perm.key")
		if err := os.WriteFile(p, bytes.Repeat([]byte{'k'}, 32), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateKey(p); err == nil {
			t.Fatalf("LoadOrCreateKey aceptó permisos %04o", perm)
		}
		_ = os.Remove(p)
	}
}

func TestURLBuildsSignedSnapshotPathWithEscapes(t *testing.T) {
	got := URL("http://127.0.0.1:8095/", "snapshots/2026-11-04/abc.jpg", "deadbeef")
	want := "http://127.0.0.1:8095/vision/snapshots/2026-11-04/abc.jpg?sig=deadbeef"
	if got != want {
		t.Fatalf("URL = %q, quiero %q", got, want)
	}

	gotEsc := URL("http://127.0.0.1:8095", "snapshots/2026-11-04/cam 1?a#b%c.jpg", "cafebabe")
	wantEsc := "http://127.0.0.1:8095/vision/snapshots/2026-11-04/cam%201%3Fa%23b%25c.jpg?sig=cafebabe"
	if gotEsc != wantEsc {
		t.Fatalf("URL con escape = %q, quiero %q", gotEsc, wantEsc)
	}
}

func TestLoadOrCreateKeyConcurrentCallersShareKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision", "snapshot-url.key")
	const callers = 16
	keys := make([][]byte, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			keys[i], errs[i] = LoadOrCreateKey(path)
		}()
	}
	close(start)
	wg.Wait()
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("llamada %d: %v", i, errs[i])
		}
		if !bytes.Equal(keys[i], keys[0]) {
			t.Fatalf("llamada %d obtuvo otra clave", i)
		}
	}
}
