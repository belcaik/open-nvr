package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	var v map[string]any
	found, err := Load(p, &v)
	if found || err != nil {
		t.Fatalf("Load inexistente = %v, %v", found, err)
	}
	if err := Save(p, map[string]any{"b": 1, "a": "é"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "{\n \"a\": \"é\",\n \"b\": 1\n}\n" {
		t.Fatalf("formato = %q", raw)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("quedó el .tmp")
	}
	found, err = Load(p, &v)
	if !found || err != nil || v["a"] != "é" {
		t.Fatalf("Load = %v %v %v", found, err, v)
	}
}
