package mqtt

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
)

type Message struct {
	Topic   string
	Payload []byte
	Retain  bool
}

type Client struct {
	URL        string
	ClientID   string
	User, Pass string
	Will       *Message
	KeepAlive  time.Duration
	OnConnect  func()
	Queue      int

	mu            sync.Mutex
	queue         chan Message
	subscriptions []subscription
	running       bool
	packetID      uint16
}

type subscription struct {
	filter  string
	handler func(Message)
}

type readResult struct {
	packet packet
	err    error
}

var (
	ErrQueueFull = errors.New("mqtt: cola de salida llena")
	retryDelay   = defaultRetryDelay
)

type ConnackError struct {
	Code byte
}

func (e *ConnackError) Error() string {
	description := map[byte]string{
		1: "versión de protocolo inaceptable",
		2: "identificador de cliente rechazado",
		3: "servidor no disponible",
		4: "usuario o contraseña incorrectos",
		5: "no autorizado",
	}[e.Code]
	if description == "" {
		description = "código desconocido"
	}
	return fmt.Sprintf("mqtt: CONNACK rechazado: %s (código %d)", description, e.Code)
}

func (c *Client) Publish(topic string, payload []byte, retain bool) error {
	if err := validateTopic(topic); err != nil {
		return err
	}
	c.mu.Lock()
	c.initQueueLocked()
	queue := c.queue
	c.mu.Unlock()
	message := Message{Topic: topic, Payload: append([]byte(nil), payload...), Retain: retain}
	select {
	case queue <- message:
		return nil
	default:
		return ErrQueueFull
	}
}

func (c *Client) Subscribe(filter string, handler func(Message)) {
	if validateFilter(filter) != nil || handler == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return
	}
	c.subscriptions = append(c.subscriptions, subscription{filter: filter, handler: handler})
}

func (c *Client) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("mqtt: contexto nil")
	}
	broker, err := parseBrokerURL(c.URL)
	if err != nil {
		return err
	}
	keepAlive := c.KeepAlive
	if keepAlive <= 0 {
		keepAlive = 30 * time.Second
	}
	if _, err := encodeConnect(c.ClientID, c.User, c.Pass, c.Will, keepAlive); err != nil {
		return err
	}
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("mqtt: el cliente ya está en ejecución")
	}
	c.running = true
	c.initQueueLocked()
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
	}()

	var pending *Message
	lastError := ""
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		conn, err := dialBroker(ctx, broker)
		connected := false
		if err == nil {
			pending, connected, err = c.runConnection(ctx, conn, keepAlive, pending)
		}
		if ctx.Err() != nil {
			return nil
		}
		if text := err.Error(); text != lastError {
			logx.Printf("mqtt: conexión perdida: %s", text)
			lastError = text
		}
		maximum := false
		var connackErr *ConnackError
		if errors.As(err, &connackErr) && (connackErr.Code == 4 || connackErr.Code == 5) {
			maximum = true
		}
		if connected {
			attempt = 0
		}
		timer := time.NewTimer(retryDelay(attempt, maximum))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		attempt++
	}
}

func (c *Client) runConnection(ctx context.Context, conn net.Conn, keepAlive time.Duration, pending *Message) (*Message, bool, error) {
	defer conn.Close()
	connect, err := encodeConnect(c.ClientID, c.User, c.Pass, c.Will, keepAlive)
	if err != nil {
		return pending, false, err
	}
	if err := writePacket(conn, connect); err != nil {
		return pending, false, err
	}
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(keepAlive + keepAlive/2))
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-handshakeDone:
		}
	}()
	response, err := readPacket(reader)
	close(handshakeDone)
	if err != nil {
		return pending, false, fmt.Errorf("mqtt: esperar CONNACK: %w", err)
	}
	if err := parseConnack(response); err != nil {
		return pending, false, err
	}
	if ctx.Err() != nil {
		c.disconnect(conn)
		return pending, true, nil
	}

	c.mu.Lock()
	subscriptions := append([]subscription(nil), c.subscriptions...)
	c.mu.Unlock()
	for _, subscription := range subscriptions {
		data, err := encodeSubscribe(c.nextPacketID(), subscription.filter)
		if err != nil {
			return pending, true, err
		}
		if err := writePacket(conn, data); err != nil {
			return pending, true, err
		}
	}
	if c.OnConnect != nil {
		c.OnConnect()
	}

	reads := make(chan readResult, 1)
	readerDone := make(chan struct{})
	defer close(readerDone)
	go func() {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(keepAlive + keepAlive/2))
			value, err := readPacket(reader)
			select {
			case reads <- readResult{packet: value, err: err}:
			case <-readerDone:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	pingTimer := time.NewTimer(keepAlive)
	defer pingTimer.Stop()
	var pongTimer *time.Timer
	var pongDeadline <-chan time.Time
	defer func() {
		if pongTimer != nil {
			pongTimer.Stop()
		}
	}()
	awaitingPong := false
	resetPing := func() {
		if !pingTimer.Stop() {
			select {
			case <-pingTimer.C:
			default:
			}
		}
		pingTimer.Reset(keepAlive)
	}
	for {
		var outgoing <-chan Message
		if pending == nil {
			outgoing = c.queue
		}
		select {
		case <-ctx.Done():
			c.disconnect(conn)
			return pending, true, nil
		case result := <-reads:
			if result.err != nil {
				return pending, true, fmt.Errorf("mqtt: leer: %w", result.err)
			}
			value := result.packet
			if err := c.handleIncoming(value, subscriptions); err != nil {
				return pending, true, err
			}
			if value.header == 0xd0 {
				awaitingPong = false
				if pongTimer != nil {
					if !pongTimer.Stop() {
						select {
						case <-pongTimer.C:
						default:
						}
					}
				}
				pongDeadline = nil
			}
		case message := <-outgoing:
			pending = &message
		case <-pingTimer.C:
			if !awaitingPong {
				if err := writePacket(conn, []byte{0xc0, 0x00}); err != nil {
					return pending, true, err
				}
				awaitingPong = true
				if pongTimer == nil {
					pongTimer = time.NewTimer(keepAlive + keepAlive/2)
				} else {
					pongTimer.Reset(keepAlive + keepAlive/2)
				}
				pongDeadline = pongTimer.C
			}
			resetPing()
		case <-pongDeadline:
			return pending, true, errors.New("mqtt: no llegó PINGRESP antes del deadline")
		}
		if pending != nil {
			data, err := encodePublish(*pending)
			if err != nil {
				return nil, true, err
			}
			if err := writePacket(conn, data); err != nil {
				return pending, true, err
			}
			pending = nil
		}
	}
}

func (c *Client) handleIncoming(value packet, subscriptions []subscription) error {
	switch value.header >> 4 {
	case 3:
		message, err := parsePublish(value)
		if err != nil {
			return err
		}
		for _, subscription := range subscriptions {
			if matchFilter(subscription.filter, message.Topic) {
				// Cada handler recibe su copia: ninguno ve lo que otro modifique.
				copied := message
				copied.Payload = append([]byte(nil), message.Payload...)
				subscription.handler(copied)
			}
		}
		return nil
	case 9:
		if value.header != 0x90 || len(value.body) < 3 || binary.BigEndian.Uint16(value.body) == 0 {
			return errors.New("mqtt: SUBACK inválido")
		}
		for _, code := range value.body[2:] {
			switch code {
			case 0x00:
			case 0x80:
				// Suele ser la ACL del broker: sin aviso no se recibiría nada.
				logx.Printf("mqtt: suscripción rechazada por el broker (packet id %d)", binary.BigEndian.Uint16(value.body))
			default:
				return errors.New("mqtt: SUBACK solicita QoS no admitido")
			}
		}
		return nil
	case 13:
		if value.header != 0xd0 || len(value.body) != 0 {
			return errors.New("mqtt: PINGRESP inválido")
		}
		return nil
	default:
		return fmt.Errorf("mqtt: paquete inesperado tipo %d", value.header>>4)
	}
}

// disconnect cierra la sesión de forma limpia. El broker descarta el will al
// recibir DISCONNECT, así que antes se publica a mano para que los
// suscriptores no sigan viendo el estado de conexión.
func (c *Client) disconnect(conn net.Conn) {
	if c.Will != nil {
		if data, err := encodePublish(*c.Will); err == nil {
			_ = writePacket(conn, data)
		}
	}
	_ = writePacket(conn, []byte{0xe0, 0x00})
}

func parseConnack(value packet) error {
	if value.header != 0x20 || len(value.body) != 2 || value.body[0] != 0 {
		return errors.New("mqtt: CONNACK inválido")
	}
	if value.body[1] != 0 {
		return &ConnackError{Code: value.body[1]}
	}
	return nil
}

func (c *Client) initQueueLocked() {
	if c.queue != nil {
		return
	}
	capacity := c.Queue
	if capacity <= 0 {
		capacity = 256
	}
	c.queue = make(chan Message, capacity)
}

func (c *Client) nextPacketID() uint16 {
	c.packetID++
	if c.packetID == 0 {
		c.packetID++
	}
	return c.packetID
}

type brokerConfig struct {
	address   string
	tlsConfig *tls.Config
}

func parseBrokerURL(raw string) (brokerConfig, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return brokerConfig{}, errors.New("mqtt: URL inválida")
	}
	if parsed.Scheme != "mqtt" && parsed.Scheme != "mqtts" {
		return brokerConfig{}, errors.New("mqtt: esquema debe ser mqtt o mqtts")
	}
	if parsed.User != nil {
		return brokerConfig{}, errors.New("mqtt: la URL no admite userinfo")
	}
	if parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return brokerConfig{}, errors.New("mqtt: URL de broker inválida")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "mqtts" {
			port = "8883"
		} else {
			port = "1883"
		}
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return brokerConfig{}, errors.New("mqtt: puerto inválido")
	}
	config := brokerConfig{address: net.JoinHostPort(parsed.Hostname(), port)}
	if parsed.Scheme == "mqtts" {
		config.tlsConfig = &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}
	}
	return config, nil
}

func dialBroker(ctx context.Context, broker brokerConfig) (net.Conn, error) {
	dialer := &net.Dialer{}
	if broker.tlsConfig != nil {
		return (&tls.Dialer{NetDialer: dialer, Config: broker.tlsConfig}).DialContext(ctx, "tcp", broker.address)
	}
	return dialer.DialContext(ctx, "tcp", broker.address)
}

func writePacket(conn net.Conn, data []byte) error {
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return fmt.Errorf("mqtt: escribir: %w", err)
		}
		if n == 0 {
			return errors.New("mqtt: escribir: no hubo progreso")
		}
		data = data[n:]
	}
	return nil
}

func defaultRetryDelay(attempt int, maximum bool) time.Duration {
	base := time.Second
	if maximum || attempt >= 6 {
		base = 60 * time.Second
	} else {
		base <<= attempt
		if base > 60*time.Second {
			base = 60 * time.Second
		}
	}
	var random [1]byte
	if _, err := rand.Read(random[:]); err != nil {
		return base
	}
	jitter := time.Duration(int(random[0])-128) * base / 1280
	return base + jitter
}
