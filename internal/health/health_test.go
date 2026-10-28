package health

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBeatAndCheck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "heartbeat")
	if err := Check(p, time.Minute, time.Now()); err == nil {
		t.Fatal("sin latido debe fallar")
	}
	if err := Beat(p); err != nil {
		t.Fatal(err)
	}
	if err := Check(p, time.Minute, time.Now()); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	os.Chtimes(p, old, old)
	if err := Check(p, time.Minute, time.Now()); err == nil {
		t.Fatal("latido antiguo debe fallar")
	}
}

func TestCheckURL(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if CheckURL(ok.URL) != nil || CheckURL(bad.URL) == nil {
		t.Fatal("CheckURL")
	}
}
