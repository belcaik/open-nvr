package egresswatch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func appendLog(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFileTailMissingFile(t *testing.T) {
	tail := NewFileTail(filepath.Join(t.TempDir(), "missing"), &TailState{})
	lines, err := tail.ReadLines(0)
	if err != nil || len(lines) != 0 {
		t.Fatalf("ReadLines() = %v, %v", lines, err)
	}
}

func TestFileTailIncrementalAndPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	state := &TailState{}
	tail := NewFileTail(path, state)
	appendLog(t, path, "a\nb\npar")
	lines, err := tail.ReadLines(0)
	if err != nil || !reflect.DeepEqual(lines, []string{"a", "b"}) {
		t.Fatalf("primera lectura = %v, %v", lines, err)
	}
	appendLog(t, path, "tial\n")
	lines, err = tail.ReadLines(0)
	if err != nil || !reflect.DeepEqual(lines, []string{"partial"}) {
		t.Fatalf("segunda lectura = %v, %v", lines, err)
	}
	lines, err = tail.ReadLines(0)
	if err != nil || len(lines) != 0 {
		t.Fatalf("tercera lectura = %v, %v", lines, err)
	}
	appendLog(t, path, "c\n")
	lines, err = NewFileTail(path, state).ReadLines(0)
	if err != nil || !reflect.DeepEqual(lines, []string{"c"}) {
		t.Fatalf("lector reconstruido = %v, %v", lines, err)
	}
}

func TestFileTailDetectsTruncationAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	tail := NewFileTail(path, &TailState{})
	appendLog(t, path, "one\ntwo\n")
	if _, err := tail.ReadLines(0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if lines, err := tail.ReadLines(0); err != nil || !reflect.DeepEqual(lines, []string{"x"}) {
		t.Fatalf("tras truncar = %v, %v", lines, err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendLog(t, path, "new\n")
	if lines, err := tail.ReadLines(0); err != nil || !reflect.DeepEqual(lines, []string{"new"}) {
		t.Fatalf("tras rotar = %v, %v", lines, err)
	}
}

func TestFileTailTruncatesOnlyAfterFullRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	tail := NewFileTail(path, &TailState{})
	appendLog(t, path, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n")
	if truncated, err := tail.TruncateIfLarger(10); err != nil || truncated {
		t.Fatalf("antes de leer = %v, %v", truncated, err)
	}
	if _, err := tail.ReadLines(0); err != nil {
		t.Fatal(err)
	}
	if truncated, err := tail.TruncateIfLarger(1000); err != nil || truncated {
		t.Fatalf("bajo límite = %v, %v", truncated, err)
	}
	if truncated, err := tail.TruncateIfLarger(10); err != nil || !truncated {
		t.Fatalf("sobre límite = %v, %v", truncated, err)
	}
	appendLog(t, path, "y\n")
	if lines, err := tail.ReadLines(0); err != nil || !reflect.DeepEqual(lines, []string{"y"}) {
		t.Fatalf("tras vaciar = %v, %v", lines, err)
	}
}
