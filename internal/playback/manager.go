// Package playback coordina sesiones históricas P6S y las presenta a go2rtc
// como fuentes HTTP efímeras. El navegador sólo recibe el nombre opaco del
// stream de go2rtc; nunca recibe el vídeo elemental del NVR directamente.
package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
	"github.com/belcaik/open-nvr/internal/p6s/history"
)

const (
	defaultMaxSessions = 4
	defaultMaxDuration = 24 * time.Hour
	defaultOpTimeout   = 10 * time.Second
	defaultMaxLifetime = 25 * time.Hour
)

var (
	ErrNotFound       = errors.New("playback: sesión no encontrada")
	ErrInvalidRequest = errors.New("playback: petición no válida")
	ErrCapacity       = errors.New("playback: límite de sesiones alcanzado")
	ErrSeekOutOfRange = errors.New("playback: seek fuera del intervalo")
)

type State string

const (
	StateReady   State = "ready"
	StatePlaying State = "playing"
	StatePaused  State = "paused"
)

type Request struct {
	Channel    int
	Start, End time.Time
}

type View struct {
	ID      string    `json:"id"`
	Stream  string    `json:"stream"`
	State   State     `json:"state"`
	Channel int       `json:"channel"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
}

// Session es la parte de history.Session que coordina el manager. La fábrica
// inyectada conserva fuera de este paquete la apertura, login y credenciales.
type Session interface {
	Create(context.Context, history.HistoryStreamCreateMultiTypeRequest) error
	Start(context.Context) error
	Seek(context.Context, history.DateTime) error
	Control(context.Context, history.ControlOperation, string) error
	Stop(context.Context) error
	Destroy(context.Context) error
	ReadMediaFrame(context.Context) (p6s.MediaFrame, error)
	Close() error
}

type OpenSession func(context.Context, Request) (Session, error)

// StreamRegistry registra fuentes en la API local de go2rtc.
type StreamRegistry interface {
	Add(ctx context.Context, name, source string) error
	Delete(ctx context.Context, name string) error
}

// StreamAuthorizer limita el proxy WebSocket a nombres temporales conocidos.
type StreamAuthorizer interface {
	AllowTemporary(name string, connected, disconnected func()) (remove func())
}

type Config struct {
	OpenSession      OpenSession
	Streams          StreamRegistry
	Authorizer       StreamAuthorizer
	SourceBaseURL    string
	Channels         []int
	MaxSessions      int
	MaxDuration      time.Duration
	OperationTimeout time.Duration
	MaxLifetime      time.Duration
}

type Manager struct {
	openSession    OpenSession
	streams        StreamRegistry
	authorizer     StreamAuthorizer
	baseURL        string
	sourcePath     string
	channels       []int
	maxSessions    int
	maxDuration    time.Duration
	opTimeout      time.Duration
	maxLifetime    time.Duration
	reconnectGrace time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	byID     map[string]*entry
	byToken  map[string]*entry
	closed   bool
	closeOne sync.Once
}

type entry struct {
	session Session
	view    View
	token   string

	ctx            context.Context
	cancel         context.CancelFunc
	release        func()
	opMu           sync.Mutex
	mu             sync.Mutex
	sourceSet      bool
	started        bool
	reconnectTimer *time.Timer
	closeOnce      sync.Once
}

func NewManager(parent context.Context, cfg Config) (*Manager, error) {
	if parent == nil {
		return nil, errors.New("playback: contexto nil")
	}
	if cfg.OpenSession == nil || cfg.Streams == nil || cfg.Authorizer == nil {
		return nil, errors.New("playback: dependencias incompletas")
	}
	if len(cfg.Channels) == 0 {
		return nil, errors.New("playback: no hay canales permitidos")
	}
	base, err := url.Parse(cfg.SourceBaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("playback: SourceBaseURL no válida")
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = defaultMaxSessions
	}
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = defaultMaxDuration
	}
	if cfg.OperationTimeout == 0 {
		cfg.OperationTimeout = defaultOpTimeout
	}
	if cfg.MaxLifetime == 0 {
		cfg.MaxLifetime = defaultMaxLifetime
	}
	if cfg.MaxSessions < 1 || cfg.MaxDuration <= 0 || cfg.OperationTimeout <= 0 || cfg.MaxLifetime <= 0 {
		return nil, errors.New("playback: límites no válidos")
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{
		openSession: cfg.OpenSession, streams: cfg.Streams, authorizer: cfg.Authorizer,
		baseURL: strings.TrimRight(cfg.SourceBaseURL, "/"), channels: append([]int(nil), cfg.Channels...),
		sourcePath:  strings.TrimRight(base.Path, "/"),
		maxSessions: cfg.MaxSessions, maxDuration: cfg.MaxDuration,
		opTimeout: cfg.OperationTimeout, maxLifetime: cfg.MaxLifetime, reconnectGrace: 15 * time.Second,
		ctx: ctx, cancel: cancel, byID: map[string]*entry{}, byToken: map[string]*entry{},
	}
	go func() {
		<-ctx.Done()
		_ = m.Close()
	}()
	return m, nil
}

func (m *Manager) Open(ctx context.Context, req Request) (View, error) {
	if err := m.validateRequest(ctx, req); err != nil {
		return View{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return View{}, errors.New("playback: manager cerrado")
	}
	if len(m.byID) >= m.maxSessions {
		m.mu.Unlock()
		return View{}, ErrCapacity
	}
	m.mu.Unlock()

	id, err := randomHex(16)
	if err != nil {
		return View{}, fmt.Errorf("playback: generar ID: %w", err)
	}
	streamToken, err := randomHex(16)
	if err != nil {
		return View{}, fmt.Errorf("playback: generar stream: %w", err)
	}
	sourceToken, err := randomHex(32)
	if err != nil {
		return View{}, fmt.Errorf("playback: generar token: %w", err)
	}
	stream := "playback_" + streamToken

	session, err := m.openSession(ctx, req)
	if err != nil {
		return View{}, fmt.Errorf("playback: abrir sesión: %w", err)
	}
	if session == nil {
		return View{}, errors.New("playback: fábrica devolvió sesión nil")
	}
	if err := session.Create(ctx, multiTypeRequest(req)); err != nil {
		_ = session.Close()
		return View{}, fmt.Errorf("playback: crear stream P6S: %w", err)
	}

	entryCtx, entryCancel := context.WithTimeout(m.ctx, m.maxLifetime)
	e := &entry{
		session: session, token: sourceToken, ctx: entryCtx, cancel: entryCancel,
		view: View{ID: id, Stream: stream, State: StateReady, Channel: req.Channel, Start: req.Start, End: req.End},
	}
	source := "ffmpeg:" + m.baseURL + "/" + sourceToken + "#input=hevc#video=h264"
	if err := m.streams.Add(ctx, stream, source); err != nil {
		_ = m.closeEntry(e)
		return View{}, fmt.Errorf("playback: registrar stream: %w", err)
	}
	// VideoRTC cierra su WebSocket al cambiar entre MSE y WebRTC; la fuente
	// HTTP de go2rtc y ClosePlayback determinan el ciclo de vida real.
	e.release = m.authorizer.AllowTemporary(stream, func() { m.connected(e) }, nil)
	m.mu.Lock()
	if m.closed || len(m.byID) >= m.maxSessions {
		m.mu.Unlock()
		_ = m.closeEntry(e)
		return View{}, ErrCapacity
	}
	m.byID[id] = e
	m.byToken[sourceToken] = e
	m.mu.Unlock()
	go func() {
		<-entryCtx.Done()
		_ = m.closeEntry(e)
	}()
	return e.snapshot(), nil
}

func (m *Manager) validateRequest(ctx context.Context, req Request) error {
	if ctx == nil {
		return errors.New("playback: contexto nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !slices.Contains(m.channels, req.Channel) {
		return fmt.Errorf("%w: canal no permitido", ErrInvalidRequest)
	}
	if req.Start.IsZero() || !req.End.After(req.Start) || req.End.Sub(req.Start) > m.maxDuration {
		return fmt.Errorf("%w: intervalo no válido", ErrInvalidRequest)
	}
	return nil
}

func multiTypeRequest(req Request) history.HistoryStreamCreateMultiTypeRequest {
	return history.HistoryStreamCreateMultiTypeRequest{
		DiskGroup: 1, Channel: req.Channel - 1, EnableEndTime: 0,
		StreamType: 63, TransportType: 1, StreamMediaType: 0,
		TaskType: 1001, Version: -1,
		BeginTime: dateTime(req.Start), EndTime: dateTime(req.End),
	}
}

func dateTime(t time.Time) history.DateTime {
	return history.DateTime{Year: t.Year(), Month: int(t.Month()), Day: t.Day(), Hour: t.Hour(), Minute: t.Minute(), Second: t.Second()}
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) Status(id string) (View, bool) {
	m.mu.Lock()
	e, ok := m.byID[id]
	m.mu.Unlock()
	if !ok {
		return View{}, false
	}
	return e.snapshot(), true
}

func (e *entry) snapshot() View {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.view
}

func (e *entry) setState(state State) {
	e.mu.Lock()
	e.view.State = state
	e.mu.Unlock()
}

func (m *Manager) Pause(ctx context.Context, id string) error {
	return m.control(ctx, id, func(ctx context.Context, e *entry) error {
		if err := e.session.Stop(ctx); err != nil {
			return err
		}
		e.setState(StatePaused)
		return nil
	})
}

func (m *Manager) Resume(ctx context.Context, id string) error {
	return m.control(ctx, id, func(ctx context.Context, e *entry) error {
		if err := e.session.Start(ctx); err != nil {
			return err
		}
		e.setState(StatePlaying)
		return nil
	})
}

func (m *Manager) Seek(ctx context.Context, id string, position time.Time) error {
	return m.control(ctx, id, func(ctx context.Context, e *entry) error {
		view := e.snapshot()
		if position.Before(view.Start) || position.After(view.End) {
			return ErrSeekOutOfRange
		}
		return e.session.Seek(ctx, dateTime(position.In(view.Start.Location())))
	})
}

func (m *Manager) control(ctx context.Context, id string, fn func(context.Context, *entry) error) error {
	if ctx == nil {
		return errors.New("playback: contexto nil")
	}
	m.mu.Lock()
	e := m.byID[id]
	m.mu.Unlock()
	if e == nil {
		return ErrNotFound
	}
	e.opMu.Lock()
	if err := ctx.Err(); err != nil {
		e.opMu.Unlock()
		return err
	}
	err := fn(ctx, e)
	e.opMu.Unlock()
	if err != nil {
		_ = m.closeEntry(e)
	}
	return err
}

func (m *Manager) ClosePlayback(id string) error {
	m.mu.Lock()
	e := m.byID[id]
	m.mu.Unlock()
	if e == nil {
		return nil
	}
	return m.closeEntry(e)
}

func (m *Manager) connected(e *entry) {
	e.mu.Lock()
	if e.reconnectTimer != nil {
		e.reconnectTimer.Stop()
		e.reconnectTimer = nil
	}
	e.mu.Unlock()
}

func (m *Manager) disconnected(e *entry) {
	e.mu.Lock()
	if e.reconnectTimer == nil {
		e.reconnectTimer = time.AfterFunc(m.reconnectGrace, func() { _ = m.closeEntry(e) })
	}
	e.mu.Unlock()
}

func (m *Manager) closeEntry(e *entry) (result error) {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		if e.reconnectTimer != nil {
			e.reconnectTimer.Stop()
			e.reconnectTimer = nil
		}
		e.mu.Unlock()
		m.mu.Lock()
		delete(m.byID, e.view.ID)
		delete(m.byToken, e.token)
		m.mu.Unlock()
		if e.release != nil {
			e.release()
		}

		e.opMu.Lock()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), m.opTimeout)
		stopErr := e.session.Stop(stopCtx)
		stopCancel()
		destroyCtx, destroyCancel := context.WithTimeout(context.Background(), m.opTimeout)
		destroyErr := e.session.Destroy(destroyCtx)
		destroyCancel()
		closeErr := e.session.Close()
		e.opMu.Unlock()
		e.cancel()

		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), m.opTimeout)
		deleteErr := m.streams.Delete(deleteCtx, e.view.Stream)
		deleteCancel()
		result = errors.Join(stopErr, destroyErr, closeErr, deleteErr)
	})
	return result
}

func (m *Manager) Close() error {
	var result error
	m.closeOne.Do(func() {
		m.mu.Lock()
		m.closed = true
		entries := make([]*entry, 0, len(m.byID))
		for _, e := range m.byID {
			entries = append(entries, e)
		}
		m.mu.Unlock()
		for _, e := range entries {
			result = errors.Join(result, m.closeEntry(e))
		}
		m.cancel()
	})
	return result
}
