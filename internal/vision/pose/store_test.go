package pose

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRefStoreRoundTripAndMissing(t *testing.T) {
	store := &RefStore{Dir: t.TempDir()}
	if _, ok, err := store.Load(2, 7, Night); err != nil || ok {
		t.Fatalf("ausente: ok=%t err=%v", ok, err)
	}
	want := Gray{W: 3, H: 2, Pix: []byte{0, 1, 2, 253, 254, 255}}
	if err := store.Save(2, 7, Night, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Load(2, 7, Night)
	if err != nil || !ok {
		t.Fatalf("carga: ok=%t err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("carga = %#v, esperada %#v", got, want)
	}
	info, err := os.Stat(filepath.Join(store.Dir, "cam2"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o750 {
		t.Fatalf("modo directorio = %o", info.Mode().Perm())
	}
}
