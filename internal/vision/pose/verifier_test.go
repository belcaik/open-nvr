package pose

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifierWithGo2RTC(t *testing.T) {
	current := readTestFrame(t, "door-day-a.jpg")
	status := http.StatusOK
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/frame.jpeg" || r.URL.Query().Get("src") != "cam3_sub" {
			t.Errorf("petición inesperada: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write(current)
		}
	})
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}

	v := &Verifier{
		Grab: &Go2RTC{Base: "http://go2rtc.test", HTTP: client},
		Refs: &RefStore{Dir: t.TempDir()},
	}
	ctx := context.Background()
	check, err := v.Verify(ctx, 3, 1)
	if err != nil || check.HasReference || check.Mode != Day {
		t.Fatalf("sin referencia: check=%+v err=%v", check, err)
	}
	mode, err := v.Capture(ctx, 3, 1)
	if err != nil || mode != Day {
		t.Fatalf("Capture: mode=%s err=%v", mode, err)
	}
	check, err = v.Verify(ctx, 3, 1)
	if err != nil || !check.HasReference || !check.Verified {
		t.Fatalf("mismo frame: check=%+v err=%v", check, err)
	}
	current = readTestFrame(t, "door-day-moved.jpg")
	check, err = v.Verify(ctx, 3, 1)
	if err != nil || check.Verified {
		t.Fatalf("frame movido: check=%+v err=%v", check, err)
	}
	status = http.StatusInternalServerError
	if _, err := v.Verify(ctx, 3, 1); err == nil {
		t.Fatal("HTTP 500 no produjo error")
	}
}

func TestGo2RTCRejectsNonJPEG(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("not a jpeg")),
		}, nil
	})}
	_, err := (&Go2RTC{Base: "http://go2rtc.test", HTTP: client}).Frame(context.Background(), 1)
	if err == nil {
		t.Fatal("respuesta no JPEG aceptada")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
