package events

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/vision"
)

type snapshotterFunc func(ctx context.Context, cam int) ([]byte, error)

func (f snapshotterFunc) Snapshot(ctx context.Context, cam int) ([]byte, error) {
	return f(ctx, cam)
}

func TestRecordStoresSnapshot(t *testing.T) {
	dir := t.TempDir()
	h := &History{Dir: dir, Snap: snapshotterFunc(func(_ context.Context, cam int) ([]byte, error) {
		if cam != 1 {
			t.Fatalf("cámara = %d", cam)
		}
		return []byte("jpeg"), nil
	})}
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.FixedZone("prueba", 2*60*60))
	ev := vision.Event{ID: "evento-1", Kind: vision.EventZoneEnter, Cam: 1, At: at}
	if _, err := h.Record(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("snapshots", "2026-11-03", "evento-1.jpg")
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "jpeg" {
		t.Fatalf("instantánea = %q", data)
	}
	stored := readStoredEvents(t, filepath.Join(dir, "events-2026-11-03.jsonl"))
	if len(stored) != 1 || stored[0].Snapshot != rel {
		t.Fatalf("eventos guardados = %+v", stored)
	}
}

func TestRecordKeepsEventWhenSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	h := &History{Dir: dir, Snap: snapshotterFunc(func(context.Context, int) ([]byte, error) {
		return nil, errors.New("go2rtc caído")
	})}
	ev := vision.Event{ID: "sin-foto", Kind: vision.EventTrackingStart, Cam: 2,
		At: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	if _, err := h.Record(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	stored := readStoredEvents(t, filepath.Join(dir, "events-2026-11-03.jsonl"))
	if len(stored) != 1 || stored[0].ID != ev.ID || stored[0].Snapshot != "" {
		t.Fatalf("eventos guardados = %+v", stored)
	}
}

func TestListFiltersSortsAndSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	h := &History{Dir: dir}
	base := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	events := []vision.Event{
		{ID: "fuera-de-rango", Kind: vision.EventZoneEnter, Cam: 1, At: base},
		{ID: "entra", Kind: vision.EventZoneEnter, Cam: 1, At: base.Add(time.Hour)},
		{ID: "tracking", Kind: vision.EventTrackingStart, Cam: 1, At: base.Add(2 * time.Hour)},
		{ID: "sale", Kind: vision.EventZoneLeave, Cam: 1, At: base.Add(3 * time.Hour)},
		{ID: "otra-camara", Kind: vision.EventZoneEnter, Cam: 2, At: base.Add(4 * time.Hour)},
	}
	for _, ev := range events {
		if _, err := h.Record(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "events-2026-11-03.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{línea corrupta\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := h.List(context.Background(), Filter{
		Cam: 1, Kinds: []vision.EventKind{vision.EventZoneEnter},
		Since: base.Add(30 * time.Minute), Until: base.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids := eventIDs(got); !reflect.DeepEqual(ids, []string{"entra"}) {
		t.Fatalf("IDs filtrados = %v", ids)
	}

	got, err = h.List(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"otra-camara", "sale", "tracking", "entra", "fuera-de-rango"}
	if ids := eventIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("orden = %v, quiero %v", ids, want)
	}
}

func TestSnapshotPathRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	h := &History{Dir: dir}
	inside := filepath.Join(dir, "snapshots", "2026-11-03")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "ok.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SnapshotPath(filepath.Join("snapshots", "2026-11-03", "ok.jpg")); err != nil {
		t.Fatalf("ruta válida rechazada: %v", err)
	}
	for _, rel := range []string{"../x", "/etc/passwd"} {
		if _, err := h.SnapshotPath(rel); err == nil {
			t.Fatalf("SnapshotPath(%q) debía fallar", rel)
		}
	}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "fuera.jpg")
	if err := os.WriteFile(outsideFile, []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(inside, "escape.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SnapshotPath(filepath.Join("snapshots", "2026-11-03", "escape.jpg")); err == nil {
		t.Fatal("el enlace simbólico fuera de Dir debía fallar")
	}
}

func TestPruneRemovesOldFilesAndKeepsToday(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	h := &History{Dir: dir, RetentionDays: 14, Now: func() time.Time { return now }}
	oldDay := "2026-10-01"
	today := "2026-11-03"
	if err := os.WriteFile(filepath.Join(dir, "events-"+oldDay+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events-"+today+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{oldDay, today} {
		path := filepath.Join(dir, "snapshots", day)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "foto.jpg"), []byte("jpg"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Prune(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(dir, "events-"+oldDay+".jsonl"),
		filepath.Join(dir, "snapshots", oldDay),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s no fue borrado: %v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(dir, "events-"+today+".jsonl"),
		filepath.Join(dir, "snapshots", today, "foto.jpg"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s debía conservarse: %v", path, err)
		}
	}
}

func readStoredEvents(t *testing.T, path string) []vision.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result []vision.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev vision.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		result = append(result, ev)
	}
	return result
}

func eventIDs(events []vision.Event) []string {
	ids := make([]string, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	return ids
}
