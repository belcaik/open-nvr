package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/vision"
)

const (
	defaultRetentionDays = 14
	defaultListLimit     = 200
	dateLayout           = "2006-01-02"
)

// Snapshotter obtiene la imagen actual de una cámara.
type Snapshotter interface {
	Snapshot(ctx context.Context, cam int) ([]byte, error)
}

// Go2RTCSnapshotter obtiene instantáneas JPEG de go2rtc.
type Go2RTCSnapshotter struct {
	Base string
	HTTP *http.Client
}

// Snapshot descarga un fotograma con un máximo de cinco segundos.
func (s Go2RTCSnapshotter) Snapshot(ctx context.Context, cam int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	url := strings.TrimRight(s.Base, "/") + fmt.Sprintf("/api/frame.jpeg?src=cam%d", cam)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("crear petición de instantánea: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("descargar instantánea: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("go2rtc respondió HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("leer instantánea: %w", err)
	}
	return data, nil
}

// History guarda eventos diarios y sus instantáneas en Dir.
type History struct {
	Dir           string
	Snap          Snapshotter
	RetentionDays int
	Now           func() time.Time

	mu sync.Mutex
}

// Filter limita los eventos devueltos por List.
type Filter struct {
	Cam   int
	Kinds []vision.EventKind
	Since time.Time
	Until time.Time
	Limit int
}

// Record añade un evento al historial y, cuando corresponde, su instantánea.
func (h *History) Record(ctx context.Context, ev vision.Event) (vision.Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ev.Snapshot = ""
	if h.Snap != nil && (ev.Kind == vision.EventZoneEnter || ev.Kind == vision.EventTrackingStart) {
		h.recordSnapshot(ctx, &ev)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return ev, fmt.Errorf("codificar evento: %w", err)
	}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return ev, fmt.Errorf("crear historial: %w", err)
	}
	path := filepath.Join(h.Dir, "events-"+ev.At.UTC().Format(dateLayout)+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ev, fmt.Errorf("abrir historial: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return ev, fmt.Errorf("escribir evento: %w", err)
	}
	if err := file.Close(); err != nil {
		return ev, fmt.Errorf("cerrar historial: %w", err)
	}
	return ev, nil
}

func (h *History) recordSnapshot(ctx context.Context, ev *vision.Event) {
	if !validSnapshotID(ev.ID) {
		logx.Printf("instantánea descartada: ID de evento inválido %q", ev.ID)
		return
	}
	data, err := h.Snap.Snapshot(ctx, ev.Cam)
	if err != nil {
		logx.Printf("no se pudo obtener instantánea de cámara %d: %v", ev.Cam, err)
		return
	}
	rel := filepath.Join("snapshots", ev.At.UTC().Format(dateLayout), ev.ID+".jpg")
	path := filepath.Join(h.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logx.Printf("no se pudo crear directorio de instantáneas: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		logx.Printf("no se pudo guardar instantánea: %v", err)
		return
	}
	ev.Snapshot = rel
}

func validSnapshotID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !filepath.IsAbs(id)
}

// List devuelve los eventos que cumplen el filtro, del más nuevo al más viejo.
func (h *History) List(ctx context.Context, filter Filter) ([]vision.Event, error) {
	entries, err := os.ReadDir(h.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []vision.Event{}, nil
		}
		return nil, fmt.Errorf("leer historial: %w", err)
	}
	kinds := make(map[vision.EventKind]struct{}, len(filter.Kinds))
	for _, kind := range filter.Kinds {
		kinds[kind] = struct{}{}
	}
	var result []vision.Event
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		events, err := h.readEvents(filepath.Join(h.Dir, name))
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			if filter.Cam != 0 && ev.Cam != filter.Cam {
				continue
			}
			if len(kinds) > 0 {
				if _, ok := kinds[ev.Kind]; !ok {
					continue
				}
			}
			if !filter.Since.IsZero() && ev.At.Before(filter.Since) {
				continue
			}
			if !filter.Until.IsZero() && ev.At.After(filter.Until) {
				continue
			}
			result = append(result, ev)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].At.After(result[j].At) })
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if len(result) > limit {
		result = result[:limit]
	}
	if result == nil {
		result = []vision.Event{}
	}
	return result, nil
}

func (h *History) readEvents(path string) ([]vision.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("abrir historial diario: %w", err)
	}
	defer file.Close()
	var result []vision.Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev vision.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		result = append(result, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("leer historial diario: %w", err)
	}
	return result, nil
}

// SnapshotPath resuelve una instantánea sin permitir salir de Dir.
func (h *History) SnapshotPath(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("ruta de instantánea inválida")
	}
	root, err := filepath.Abs(h.Dir)
	if err != nil {
		return "", fmt.Errorf("resolver historial: %w", err)
	}
	candidate := filepath.Join(root, rel)
	if outside(root, candidate) {
		return "", fmt.Errorf("la instantánea sale del historial")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolver directorio de instantáneas: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolver instantánea: %w", err)
	}
	if outside(resolvedRoot, resolved) {
		return "", fmt.Errorf("la instantánea sale del historial")
	}
	return resolved, nil
}

func outside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Prune elimina historiales e instantáneas anteriores al periodo de retención.
func (h *History) Prune() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	days := h.RetentionDays
	if days <= 0 {
		days = defaultRetentionDays
	}
	cutoff := h.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	entries, err := os.ReadDir(h.Dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("leer historial para purgar: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "events-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		day, err := time.Parse(dateLayout, strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "events-"), ".jsonl"))
		if err == nil && day.Before(cutoff) {
			if err := os.Remove(filepath.Join(h.Dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("borrar historial antiguo: %w", err)
			}
		}
	}

	snapshotsDir := filepath.Join(h.Dir, "snapshots")
	entries, err = os.ReadDir(snapshotsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("leer instantáneas para purgar: %w", err)
	}
	for _, entry := range entries {
		day, err := time.Parse(dateLayout, entry.Name())
		if err != nil || !day.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(snapshotsDir, entry.Name())); err != nil {
			return fmt.Errorf("borrar instantáneas antiguas: %w", err)
		}
	}
	return nil
}

func (h *History) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
