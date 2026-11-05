// Package snapsign firma y verifica rutas de instantáneas de visión mediante
// HMAC-SHA256. El valor rel que se firma y verifica es exactamente
// vision.Event.Snapshot (por ejemplo, "snapshots/2026-11-04/abc.jpg").
package snapsign

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const keySize = 32

// Signer calcula y comprueba firmas HMAC-SHA256 sobre rutas de instantáneas.
type Signer struct {
	Key []byte
}

// Sign devuelve el HMAC-SHA256 en hexadecimal minúsculo (64 caracteres) de rel
// (exactamente vision.Event.Snapshot, p. ej. "snapshots/2026-11-04/abc.jpg").
// Si la clave está vacía, devuelve "".
func (s Signer) Sign(rel string) string {
	if len(s.Key) == 0 {
		return ""
	}
	return hex.EncodeToString(s.mac(rel))
}

// Verify comprueba en tiempo constante que sig sea la firma hexadecimal en
// minúsculas de rel (exactamente vision.Event.Snapshot). Devuelve false si la
// clave está vacía o si sig está mal formada.
func (s Signer) Verify(rel, sig string) bool {
	if len(s.Key) == 0 || len(sig) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(sig); i++ {
		c := sig[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, s.mac(rel))
}

func (s Signer) mac(rel string) []byte {
	m := hmac.New(sha256.New, s.Key)
	m.Write([]byte(rel))
	return m.Sum(nil)
}

// LoadOrCreateKey carga una clave de 32 bytes desde path o, si no existe, crea
// el directorio padre (0750) y genera un fichero nuevo (0600, O_EXCL) con
// crypto/rand. Rechaza ficheros con tamaño distinto de 32 bytes o con permisos
// más abiertos que 0600.
func LoadOrCreateKey(path string) ([]byte, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return createKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("abrir clave de instantáneas: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspeccionar clave de instantáneas: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("clave de instantáneas no es un fichero regular: %s", path)
	}
	if perm := info.Mode().Perm(); perm&^0o600 != 0 {
		return nil, fmt.Errorf("permisos de clave demasiado abiertos (%04o, máximo 0600): %s", perm, path)
	}
	if info.Size() != keySize {
		return nil, fmt.Errorf("tamaño de clave inválido (%d bytes, se esperaban %d): %s", info.Size(), keySize, path)
	}
	key, err := io.ReadAll(io.LimitReader(f, keySize+1))
	if err != nil {
		return nil, fmt.Errorf("leer clave de instantáneas: %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("tamaño de clave inválido (%d bytes, se esperaban %d): %s", len(key), keySize, path)
	}
	return key, nil
}

func createKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("crear directorio de clave de instantáneas: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generar clave de instantáneas: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("crear fichero de clave de instantáneas: %w", err)
	}
	_, writeErr := f.Write(key)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, fmt.Errorf("escribir clave de instantáneas: %w", writeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("cerrar clave de instantáneas: %w", closeErr)
	}
	return key, nil
}

// URL construye la URL firmada de una instantánea a partir de base, rel
// (vision.Event.Snapshot, con o sin prefijo "snapshots/") y sig.
func URL(base, rel, sig string) string {
	trimmed := strings.TrimPrefix(rel, "snapshots/")
	segments := strings.Split(trimmed, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	return strings.TrimRight(base, "/") + "/vision/snapshots/" + strings.Join(segments, "/") + "?sig=" + url.QueryEscape(sig)
}
