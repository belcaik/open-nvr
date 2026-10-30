package playback

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
	"github.com/belcaik/open-nvr/internal/p6s/history"
)

type fakeSession struct {
	mu        sync.Mutex
	created   history.HistoryStreamCreateMultiTypeRequest
	calls     []string
	frames    chan p6s.MediaFrame
	closed    chan struct{}
	closeOnce sync.Once
	seekAt    history.DateTime
	seekErr   error
}

func newFakeSession() *fakeSession {
	return &fakeSession{frames: make(chan p6s.MediaFrame, 4), closed: make(chan struct{})}
}

func (s *fakeSession) Create(_ context.Context, r history.HistoryStreamCreateMultiTypeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = r
	s.calls = append(s.calls, "create")
	return nil
}
func (s *fakeSession) Start(context.Context) error { return s.call("start") }
func (s *fakeSession) Seek(_ context.Context, at history.DateTime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "seek")
	s.seekAt = at
	return s.seekErr
}
func (s *fakeSession) Control(context.Context, history.ControlOperation, string) error {
	return s.call("speed")
}
func (s *fakeSession) Stop(context.Context) error    { return s.call("stop") }
func (s *fakeSession) Destroy(context.Context) error { return s.call("destroy") }
func (s *fakeSession) Close() error {
	s.call("close")
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}
func (s *fakeSession) ReadMediaFrame(ctx context.Context) (p6s.MediaFrame, error) {
	select {
	case f := <-s.frames:
		return f, nil
	case <-s.closed:
		return p6s.MediaFrame{}, errors.New("closed")
	case <-ctx.Done():
		return p6s.MediaFrame{}, ctx.Err()
	}
}
func (s *fakeSession) call(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
	return nil
}
func (s *fakeSession) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

type fakeRegistry struct {
	mu      sync.Mutex
	added   map[string]string
	deleted []string
	addErr  error
}

func (r *fakeRegistry) Add(_ context.Context, name, source string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.added == nil {
		r.added = map[string]string{}
	}
	r.added[name] = source
	return r.addErr
}
func (r *fakeRegistry) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, name)
	return nil
}

type fakeAuthorizer struct {
	mu      sync.Mutex
	allowed map[string]func()
}

func (a *fakeAuthorizer) AllowTemporary(name string, _, disconnected func()) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.allowed == nil {
		a.allowed = map[string]func(){}
	}
	a.allowed[name] = disconnected
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.allowed, name)
	}
}

func newManagerForTest(t *testing.T, s *fakeSession) (*Manager, *fakeRegistry, *fakeAuthorizer) {
	t.Helper()
	reg := &fakeRegistry{}
	authz := &fakeAuthorizer{}
	m, err := NewManager(context.Background(), Config{
		OpenSession: func(context.Context, Request) (Session, error) { return s, nil },
		Streams:     reg, Authorizer: authz, SourceBaseURL: "http://127.0.0.1:8095/internal/playback",
		Channels: []int{1, 2}, MaxSessions: 2, MaxDuration: 24 * time.Hour,
		OperationTimeout: time.Second, MaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.reconnectGrace = 20 * time.Millisecond
	t.Cleanup(func() { _ = m.Close() })
	return m, reg, authz
}

func TestManagerOpensMultiTypeAndRegistersOpaqueFFmpegSource(t *testing.T) {
	s := newFakeSession()
	m, reg, authz := newManagerForTest(t, s)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.FixedZone("CLST", -3*3600))
	view, err := m.Open(context.Background(), Request{Channel: 2, Start: start, End: start.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.Stream == "" || view.State != StateReady {
		t.Fatalf("view = %+v", view)
	}
	if view.ID == view.Stream {
		t.Fatal("el ID web y el stream no deben compartir token")
	}
	if got := s.created; got.Channel != 1 || got.StreamType != 63 || got.TaskType != 1001 || got.BeginTime.Hour != 12 {
		t.Fatalf("Create = %+v", got)
	}
	reg.mu.Lock()
	source := reg.added[view.Stream]
	reg.mu.Unlock()
	if len(source) < len("ffmpeg:http://127.0.0.1:8095/internal/playback/")+32 || !bytes.HasPrefix([]byte(source), []byte("ffmpeg:http://127.0.0.1:8095/internal/playback/")) || !bytes.HasSuffix([]byte(source), []byte("#input=hevc#video=h264")) {
		t.Fatalf("source temporal inválida: %q", source)
	}
	authz.mu.Lock()
	_, ok := authz.allowed[view.Stream]
	authz.mu.Unlock()
	if !ok {
		t.Fatal("el proxy no autorizó el stream temporal")
	}
}

func TestSourceStreamsOnlyPayloadAndDisconnectClosesPlayback(t *testing.T) {
	s := newFakeSession()
	m, reg, _ := newManagerForTest(t, s)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	reg.mu.Lock()
	source := reg.added[view.Stream]
	reg.mu.Unlock()
	token := source[len("ffmpeg:http://127.0.0.1:8095/internal/playback/") : len(source)-len("#input=hevc#video=h264")]

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/internal/playback/"+token, nil).WithContext(ctx)
	rec := newSignalWriter()
	done := make(chan struct{})
	go func() { m.SourceHandler().ServeHTTP(rec, req); close(done) }()
	s.frames <- p6s.MediaFrame{Extension: []byte{1, 2, 3, 4}}
	s.frames <- p6s.MediaFrame{Extension: make([]byte, 44), Payload: []byte{0, 0, 1, 0xb7, 9, 0, 0, 0, 1, 0x40, 1, 9}}
	select {
	case <-rec.wrote:
	case <-time.After(time.Second):
		t.Fatal("la fuente no escribió el frame")
	}
	if got := rec.bytes(); !bytes.Equal(got, []byte{0, 0, 0, 1, 0x40, 1, 9}) {
		t.Fatalf("body = %x", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("la fuente no terminó al desconectar")
	}
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("la sesión no se cerró tras el período de gracia")
	}
	want := []string{"create", "stop", "seek", "start", "speed", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if _, ok := m.Status(view.ID); ok {
		t.Fatal("la sesión cerrada sigue visible")
	}
}

func TestSourceReconnectsWithoutRepeatingHandshake(t *testing.T) {
	s := newFakeSession()
	m, reg, _ := newManagerForTest(t, s)
	m.reconnectGrace = time.Second
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	reg.mu.Lock()
	source := reg.added[view.Stream]
	reg.mu.Unlock()
	token := source[len("ffmpeg:http://127.0.0.1:8095/internal/playback/") : len(source)-len("#input=hevc#video=h264")]

	serve := func(ctx context.Context) (*signalWriter, chan struct{}) {
		req := httptest.NewRequest(http.MethodGet, "/internal/playback/"+token, nil).WithContext(ctx)
		rec := newSignalWriter()
		done := make(chan struct{})
		go func() { m.SourceHandler().ServeHTTP(rec, req); close(done) }()
		return rec, done
	}
	waitDone := func(done chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("la fuente no terminó")
		}
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	rec1, done1 := serve(ctx1)
	s.frames <- p6s.MediaFrame{Extension: make([]byte, 4)}
	s.frames <- p6s.MediaFrame{Extension: make([]byte, 44), Payload: []byte{0, 0, 1, 0xb7, 9, 0, 0, 0, 1, 0x40, 1, 9}}
	<-rec1.wrote
	cancel1()
	waitDone(done1)
	if _, ok := m.Status(view.ID); !ok {
		t.Fatal("la sesión no sobrevivió al período de gracia")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	rec2, done2 := serve(ctx2)
	s.frames <- p6s.MediaFrame{Extension: make([]byte, 44), Payload: []byte{0, 0, 0, 1, 0x40, 1, 9}}
	select {
	case <-rec2.wrote:
	case <-time.After(time.Second):
		t.Fatal("la fuente reconectada no escribió")
	}
	cancel2()
	waitDone(done2)
	if err := m.ClosePlayback(view.ID); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "stop", "seek", "start", "speed", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

type signalWriter struct {
	mu    sync.Mutex
	head  http.Header
	body  bytes.Buffer
	wrote chan struct{}
	once  sync.Once
}

func newSignalWriter() *signalWriter {
	return &signalWriter{head: http.Header{}, wrote: make(chan struct{})}
}
func (w *signalWriter) Header() http.Header { return w.head }
func (w *signalWriter) WriteHeader(int)     {}
func (w *signalWriter) Flush()              {}
func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.body.Write(p)
	w.mu.Unlock()
	w.once.Do(func() { close(w.wrote) })
	return n, err
}
func (w *signalWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.body.Bytes()...)
}

func TestManagerControlsAndClosesSession(t *testing.T) {
	s := newFakeSession()
	m, _, _ := newManagerForTest(t, s)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Resume(context.Background(), view.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Pause(context.Background(), view.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Status(view.ID); got.State != StatePaused {
		t.Fatalf("state = %q", got.State)
	}
	if err := m.Seek(context.Background(), view.ID, start.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := m.ClosePlayback(view.ID); err != nil {
		t.Fatal(err)
	}
	want := []string{"create", "start", "stop", "seek", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestManagerCleansP6SWhenGo2RTCRegistrationFails(t *testing.T) {
	s := newFakeSession()
	m, reg, _ := newManagerForTest(t, s)
	reg.addErr = errors.New("go2rtc unavailable")
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if _, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Hour)}); err == nil {
		t.Fatal("Open debía fallar")
	}
	want := []string{"create", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestManagerConvertsSeekInstantToRecordingTimezone(t *testing.T) {
	s := newFakeSession()
	m, _, _ := newManagerForTest(t, s)
	loc := time.FixedZone("CLST", -3*60*60)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, loc)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// El navegador serializa Date como UTC; P6S necesita el reloj local del NVR.
	if err := m.Seek(context.Background(), view.ID, time.Date(2026, 10, 28, 15, 10, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if s.seekAt.Hour != 12 || s.seekAt.Minute != 10 {
		t.Fatalf("seek P6S = %+v", s.seekAt)
	}
}

func TestManagerClosesSessionAfterControlError(t *testing.T) {
	s := newFakeSession()
	s.seekErr = errors.New("seek rejected")
	m, _, _ := newManagerForTest(t, s)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Seek(context.Background(), view.ID, start.Add(time.Minute)); err == nil {
		t.Fatal("Seek debía fallar")
	}
	want := []string{"create", "seek", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestManagerRejectsUnknownChannelAndOversizedIntervalBeforeOpening(t *testing.T) {
	s := newFakeSession()
	m, _, _ := newManagerForTest(t, s)
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, req := range []Request{
		{Channel: 9, Start: start, End: start.Add(time.Hour)},
		{Channel: 1, Start: start, End: start.Add(25 * time.Hour)},
	} {
		if _, err := m.Open(context.Background(), req); err == nil {
			t.Fatalf("aceptó %+v", req)
		}
	}
	if got := s.snapshot(); len(got) != 0 {
		t.Fatalf("abrió P6S: %v", got)
	}
}

func TestManagerEnforcesCapacityBeforeOpeningAnotherSession(t *testing.T) {
	opened := 0
	reg := &fakeRegistry{}
	authz := &fakeAuthorizer{}
	m, err := NewManager(context.Background(), Config{
		OpenSession: func(context.Context, Request) (Session, error) {
			opened++
			return newFakeSession(), nil
		},
		Streams: reg, Authorizer: authz, SourceBaseURL: "http://127.0.0.1:8095/internal/playback",
		Channels: []int{1}, MaxSessions: 1, MaxDuration: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if _, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Minute)}); err == nil {
		t.Fatal("aceptó más sesiones que MaxSessions")
	}
	if opened != 1 {
		t.Fatalf("abrió %d sesiones P6S", opened)
	}
}

func TestManagerRootCancellationStopsAndDestroysPlayback(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	s := newFakeSession()
	reg := &fakeRegistry{}
	authz := &fakeAuthorizer{}
	m, err := NewManager(parent, Config{
		OpenSession: func(context.Context, Request) (Session, error) { return s, nil },
		Streams:     reg, Authorizer: authz, SourceBaseURL: "http://127.0.0.1:8095/internal/playback",
		Channels: []int{1}, MaxSessions: 1, MaxDuration: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	view, err := m.Open(context.Background(), Request{Channel: 1, Start: start, End: start.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := m.Status(view.ID); !ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := m.Status(view.ID); ok {
		t.Fatal("la cancelación no retiró la sesión")
	}
	want := []string{"create", "stop", "destroy", "close"}
	if got := s.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
