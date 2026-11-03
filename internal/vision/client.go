package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/coder/websocket"
)

var (
	clientReconnectBase = time.Second
	clientReconnectMax  = 30 * time.Second
)

// ConnectionState es el estado observable de la conexión WebSocket.
type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
)

// Client mantiene la conexión con el detector.
type Client struct {
	url        string
	httpClient *http.Client

	mu             sync.Mutex
	started        bool
	frames         chan Frame
	statuses       chan Status
	enabled        []int
	enabledSet     bool
	enabledVersion uint64
	enabledPending []enabledUpdate
	enabledChanged chan struct{}
	state          atomic.Uint32
}

type enabledUpdate struct {
	version uint64
	cameras []int
}

// NewClient crea un cliente para la URL WebSocket indicada.
func NewClient(url string) *Client {
	return &Client{url: url, enabledChanged: make(chan struct{}, 1)}
}

// NewClientWithHTTPClient permite personalizar el transporte HTTP del handshake.
func NewClientWithHTTPClient(url string, httpClient *http.Client) *Client {
	return &Client{url: url, httpClient: httpClient, enabledChanged: make(chan struct{}, 1)}
}

// SetEnabled fija las cámaras que debe procesar el detector.
func (c *Client) SetEnabled(cams []int) {
	enabled := make([]int, len(cams))
	copy(enabled, cams)
	sort.Ints(enabled)
	enabled = slices.Compact(enabled)
	c.mu.Lock()
	if c.enabledSet && slices.Equal(c.enabled, enabled) {
		c.mu.Unlock()
		return
	}
	c.enabled, c.enabledSet = enabled, true
	c.enabledVersion++
	c.enabledPending = append(c.enabledPending, enabledUpdate{version: c.enabledVersion, cameras: enabled})
	c.mu.Unlock()
	select {
	case c.enabledChanged <- struct{}{}:
	default:
	}
}

// Start inicia el cliente. Cada instancia se puede iniciar una sola vez.
func (c *Client) Start(ctx context.Context) (<-chan Frame, <-chan Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return c.frames, c.statuses
	}
	c.started = true
	c.frames = make(chan Frame, 16)
	c.statuses = make(chan Status, 16)
	go c.loop(ctx)
	return c.frames, c.statuses
}

// Frames devuelve el canal tras iniciar el cliente.
func (c *Client) Frames() <-chan Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames
}

// Statuses devuelve el canal tras iniciar el cliente.
func (c *Client) Statuses() <-chan Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statuses
}

// State devuelve el estado actual de conexión.
func (c *Client) State() ConnectionState {
	switch c.state.Load() {
	case 1:
		return StateConnecting
	case 2:
		return StateConnected
	default:
		return StateDisconnected
	}
}

// Connected indica si el WebSocket está conectado.
func (c *Client) Connected() bool {
	return c.State() == StateConnected
}

func (c *Client) setState(state ConnectionState) {
	var value uint32
	switch state {
	case StateConnecting:
		value = 1
	case StateConnected:
		value = 2
	}
	c.state.Store(value)
}

func (c *Client) loop(ctx context.Context) {
	defer close(c.frames)
	defer close(c.statuses)
	defer c.setState(StateDisconnected)
	wait := clientReconnectBase
	for ctx.Err() == nil {
		c.setState(StateConnecting)
		connected, err := c.readConnection(ctx)
		if ctx.Err() != nil {
			return
		}
		c.setState(StateDisconnected)
		if err != nil {
			logx.Printf("vision: conexión cerrada: %v", err)
		}
		if connected {
			wait = clientReconnectBase
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if !connected {
			wait *= 2
			if wait > clientReconnectMax {
				wait = clientReconnectMax
			}
		}
	}
}

func (c *Client) readConnection(ctx context.Context) (bool, error) {
	conn, _, err := websocket.Dial(ctx, c.url, &websocket.DialOptions{HTTPClient: c.httpClient})
	if err != nil {
		return false, fmt.Errorf("conectar al detector: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "fin")
	conn.SetReadLimit(1 << 20)
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	version, err := c.writeEnabled(ctx, conn, 0)
	if err != nil {
		return true, err
	}
	c.setState(StateConnected)
	c.discardEnabled(version)
	type readResult struct {
		messageType websocket.MessageType
		data        []byte
		err         error
	}
	reads := make(chan readResult, 1)
	go func() {
		for {
			messageType, data, err := conn.Read(readCtx)
			select {
			case reads <- readResult{messageType: messageType, data: data, err: err}:
			case <-readCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-c.enabledChanged:
			for {
				update, ok := c.nextEnabled(version)
				if !ok {
					break
				}
				if err := writeEnabled(ctx, conn, update.cameras); err != nil {
					return true, err
				}
				version = update.version
			}
		case result := <-reads:
			if result.err != nil {
				return true, fmt.Errorf("leer del detector: %w", result.err)
			}
			if result.messageType != websocket.MessageText {
				continue
			}
			message, err := Decode(result.data)
			if err != nil {
				logx.Printf("vision: mensaje inválido descartado: %v", err)
				continue
			}
			switch value := message.(type) {
			case Frame:
				select {
				case c.frames <- value:
				case <-ctx.Done():
					return true, ctx.Err()
				}
			case Status:
				select {
				case c.statuses <- value:
				case <-ctx.Done():
					return true, ctx.Err()
				}
			}
		}
	}
}

func (c *Client) writeEnabled(ctx context.Context, conn *websocket.Conn, previous uint64) (uint64, error) {
	c.mu.Lock()
	enabled := make([]int, len(c.enabled))
	copy(enabled, c.enabled)
	set, version := c.enabledSet, c.enabledVersion
	c.mu.Unlock()
	if !set || version == previous {
		return version, nil
	}
	if err := writeEnabled(ctx, conn, enabled); err != nil {
		return previous, err
	}
	return version, nil
}

func writeEnabled(ctx context.Context, conn *websocket.Conn, enabled []int) error {
	data, err := json.Marshal(struct {
		Type    string `json:"type"`
		Enabled []int  `json:"enabled"`
	}{Type: "cameras", Enabled: enabled})
	if err != nil {
		return fmt.Errorf("codificar cámaras habilitadas: %w", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		return fmt.Errorf("enviar cámaras habilitadas: %w", err)
	}
	return nil
}

func (c *Client) discardEnabled(version uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.enabledPending) > 0 && c.enabledPending[0].version <= version {
		c.enabledPending = c.enabledPending[1:]
	}
}

func (c *Client) nextEnabled(previous uint64) (enabledUpdate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.enabledPending) > 0 && c.enabledPending[0].version <= previous {
		c.enabledPending = c.enabledPending[1:]
	}
	if len(c.enabledPending) == 0 {
		return enabledUpdate{}, false
	}
	update := c.enabledPending[0]
	c.enabledPending = c.enabledPending[1:]
	return update, true
}
