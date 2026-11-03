package pose

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// RefStore persiste imágenes de referencia en formato PGM.
type RefStore struct {
	Dir string
}

// Exists indica si existe la referencia sin cargarla en memoria.
func (s *RefStore) Exists(cam, preset int, mode Mode) bool {
	_, err := os.Stat(s.path(cam, preset, mode))
	return err == nil
}

// Save guarda una referencia mediante reemplazo atómico.
func (s *RefStore) Save(cam, preset int, mode Mode, g Gray) error {
	if err := validGray(g); err != nil {
		return err
	}
	dir := filepath.Join(s.Dir, fmt.Sprintf("cam%d", cam))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("pose: crear directorio de referencias: %w", err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return fmt.Errorf("pose: ajustar directorio de referencias: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".pose-*.pgm")
	if err != nil {
		return fmt.Errorf("pose: crear referencia temporal: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	writeErr := tmp.Chmod(0o600)
	if writeErr == nil {
		_, writeErr = fmt.Fprintf(tmp, "P5\n%d %d\n255\n", g.W, g.H)
	}
	if writeErr == nil {
		_, writeErr = tmp.Write(g.Pix)
	}
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("pose: escribir referencia: %w", writeErr)
	}
	if err := os.Rename(tmpName, s.path(cam, preset, mode)); err != nil {
		return fmt.Errorf("pose: reemplazar referencia: %w", err)
	}
	return nil
}

// Load carga una referencia si existe.
func (s *RefStore) Load(cam, preset int, mode Mode) (Gray, bool, error) {
	f, err := os.Open(s.path(cam, preset, mode))
	if errors.Is(err, fs.ErrNotExist) {
		return Gray{}, false, nil
	}
	if err != nil {
		return Gray{}, false, fmt.Errorf("pose: abrir referencia: %w", err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var magic string
	var w, h, maxValue int
	if _, err := fmt.Fscan(r, &magic, &w, &h, &maxValue); err != nil {
		return Gray{}, false, fmt.Errorf("pose: leer cabecera PGM: %w", err)
	}
	if magic != "P5" || w <= 0 || h <= 0 || maxValue != 255 {
		return Gray{}, false, errors.New("pose: referencia PGM no válida")
	}
	separator, err := r.ReadByte()
	if err != nil {
		return Gray{}, false, fmt.Errorf("pose: leer separador PGM: %w", err)
	}
	if separator != '\n' && separator != '\r' && separator != ' ' && separator != '\t' {
		return Gray{}, false, errors.New("pose: separador PGM no válido")
	}
	g := Gray{W: w, H: h, Pix: make([]byte, w*h)}
	if _, err := io.ReadFull(r, g.Pix); err != nil {
		return Gray{}, false, fmt.Errorf("pose: leer píxeles PGM: %w", err)
	}
	return g, true, nil
}

func (s *RefStore) path(cam, preset int, mode Mode) string {
	return filepath.Join(s.Dir, fmt.Sprintf("cam%d", cam), fmt.Sprintf("preset%d-%s.pgm", preset, mode))
}

func validGray(g Gray) error {
	if g.W <= 0 || g.H <= 0 || len(g.Pix) != g.W*g.H {
		return errors.New("pose: imagen gris no válida")
	}
	return nil
}
