package vision

import (
	"context"
	"fmt"
	"net/http"
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

	mu       sync.Mutex
	started  bool
	frames   chan Frame
	statuses chan Status
	state    atomic.Uint32
}

// NewClient crea un cliente para la URL WebSocket indicada.
func NewClient(url string) *Client {
	return &Client{url: url}
}

// NewClientWithHTTPClient permite personalizar el transporte HTTP del handshake.
func NewClientWithHTTPClient(url string, httpClient *http.Client) *Client {
	return &Client{url: url, httpClient: httpClient}
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
	c.setState(StateConnected)
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			return true, fmt.Errorf("leer del detector: %w", err)
		}
		if messageType != websocket.MessageText {
			continue
		}
		message, err := Decode(data)
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
