package mqtt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
)

func TestRunSendsConnectWithCredentialsAndWill(t *testing.T) {
	listener := listenBroker(t)
	packets := make(chan packet, 3)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		connect, err := readPacket(reader)
		if err != nil {
			return
		}
		packets <- connect
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		for range 2 {
			next, err := readPacket(reader)
			if err != nil {
				return
			}
			packets <- next
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		URL:       brokerURL(listener),
		ClientID:  "cliente-prueba",
		User:      "usuario",
		Pass:      "secreto-no-mostrar",
		Will:      &Message{Topic: "opennvr/status", Payload: []byte("offline"), Retain: true},
		OnConnect: cancel,
	}
	if err := client.Run(ctx); err != nil {
		t.Fatal(err)
	}

	connect := receive(t, packets)
	if connect.header != 0x10 {
		t.Fatalf("cabecera CONNECT = %#x", connect.header)
	}
	protocol, rest, err := takeString(connect.body)
	if err != nil || protocol != "MQTT" || len(rest) < 4 || rest[0] != 4 {
		t.Fatalf("CONNECT inválido: protocolo=%q resto=%x error=%v", protocol, rest, err)
	}
	flags := rest[1]
	if flags != 0xe6 {
		t.Fatalf("flags CONNECT = %#x; quiere 0xe6", flags)
	}
	values := takeStrings(t, rest[4:], 5)
	want := []string{"cliente-prueba", "opennvr/status", "offline", "usuario", "secreto-no-mostrar"}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("campo CONNECT %d = %q; quiere %q", i, values[i], want[i])
		}
	}
	// Un DISCONNECT descarta el will en el broker: antes se publica a mano.
	will := receive(t, packets)
	if topic, payload, err := takeString(will.body); will.header != 0x31 || err != nil || topic != "opennvr/status" || string(payload) != "offline" {
		t.Fatalf("will antes de DISCONNECT = %#x topic %q payload %q error %v", will.header, topic, payload, err)
	}
	if disconnect := receive(t, packets); disconnect.header != 0xe0 || len(disconnect.body) != 0 {
		t.Fatalf("DISCONNECT = %#x %x", disconnect.header, disconnect.body)
	}
}

func TestConnackRejectedReturnsTypedError(t *testing.T) {
	t.Parallel()

	err := parseConnack(packet{header: 0x20, body: []byte{0x00, 0x05}})
	var connackErr *ConnackError
	if !errors.As(err, &connackErr) || connackErr.Code != 5 {
		t.Fatalf("error = %#v; quiere ConnackError código 5", err)
	}
	if !strings.Contains(err.Error(), "no autorizado") {
		t.Fatalf("texto = %q; quiere mensaje en español", err)
	}
}

func TestRejectedConnackRetriesAtMaximumBackoffAndLogsOnce(t *testing.T) {
	listener := listenBroker(t)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = readPacket(bufio.NewReader(conn))
			_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x05})
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	oldRetryDelay := retryDelay
	var retries atomic.Int32
	var notMaximum atomic.Bool
	retryDelay = func(_ int, maximum bool) time.Duration {
		if !maximum {
			notMaximum.Store(true)
		}
		if retries.Add(1) == 2 {
			cancel()
		}
		return 0
	}
	t.Cleanup(func() { retryDelay = oldRetryDelay })
	oldOutput := logx.Out
	var output bytes.Buffer
	logx.Out = &output
	t.Cleanup(func() { logx.Out = oldOutput })

	client := &Client{URL: brokerURL(listener), ClientID: "rechazado", Pass: "secreto-no-mostrar"}
	if err := client.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if notMaximum.Load() {
		t.Error("CONNACK 5 no pidió el backoff máximo")
	}
	if got := strings.Count(output.String(), "CONNACK rechazado"); got != 1 {
		t.Fatalf("registro contiene %d rechazos; quiere 1:\n%s", got, output.String())
	}
	if strings.Contains(output.String(), client.Pass) {
		t.Fatalf("registro filtra contraseña: %s", output.String())
	}
}

func TestPublishIsBoundedAndCopiesPayload(t *testing.T) {
	t.Parallel()

	client := &Client{Queue: 1}
	payload := []byte("original")
	if err := client.Publish("casa/estado", payload, true); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	if err := client.Publish("casa/otro", nil, false); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("segundo Publish = %v; quiere ErrQueueFull", err)
	}
	queued := <-client.queue
	if string(queued.Payload) != "original" {
		t.Fatalf("payload encolado = %q", queued.Payload)
	}
}

func TestQueuedRetainedPublishIsSentAfterConnect(t *testing.T) {
	listener := listenBroker(t)
	received := make(chan packet, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = readPacket(reader)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		publish, err := readPacket(reader)
		if err == nil {
			received <- publish
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{URL: brokerURL(listener), ClientID: "publicador"}
	if err := client.Publish("casa/estado", []byte("online"), true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	publish := receive(t, received)
	if publish.header != 0x31 {
		t.Fatalf("cabecera PUBLISH = %#x; quiere retain QoS 0", publish.header)
	}
	topic, payload, err := takeString(publish.body)
	if err != nil || topic != "casa/estado" || string(payload) != "online" {
		t.Fatalf("PUBLISH = topic %q payload %q error %v", topic, payload, err)
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestURLValidation(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{"http://broker", "mqtt://usuario:secreto@broker", "mqtt://", "://"} {
		client := &Client{URL: rawURL, Pass: "secreto-no-mostrar"}
		err := client.Run(context.Background())
		if err == nil {
			t.Errorf("Run con URL %q no devolvió error", rawURL)
		} else if strings.Contains(err.Error(), client.Pass) {
			t.Errorf("error filtra contraseña: %v", err)
		}
	}
	for _, tc := range []struct {
		rawURL  string
		address string
		tls     bool
	}{
		{"mqtt://broker", "broker:1883", false},
		{"mqtts://broker", "broker:8883", true},
		{"mqtt://[::1]:2883", "[::1]:2883", false},
	} {
		cfg, err := parseBrokerURL(tc.rawURL)
		if err != nil || cfg.address != tc.address || (cfg.tlsConfig != nil) != tc.tls {
			t.Errorf("parseBrokerURL(%q) = %#v, %v", tc.rawURL, cfg, err)
		}
		if tc.tls && (cfg.tlsConfig.ServerName != "broker" || cfg.tlsConfig.MinVersion != tls.VersionTLS12) {
			t.Errorf("configuración TLS = %#v", cfg.tlsConfig)
		}
	}
}

func TestSubscribeDeliversMatchingMessagesInArrivalOrder(t *testing.T) {
	listener := listenBroker(t)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = readPacket(reader)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		subscribe, err := readPacket(reader)
		if err != nil || subscribe.header != 0x82 || len(subscribe.body) < 2 {
			return
		}
		id := subscribe.body[:2]
		_, _ = conn.Write([]byte{0x90, 0x03, id[0], id[1], 0x00})
		_, _ = conn.Write(serverPublish("casa/cocina/estado", "uno", false))
		_, _ = conn.Write(serverPublish("jardín/estado", "ignorar", false))
		_, _ = conn.Write(serverPublish("casa/salón/estado", "dos", true))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan Message, 2)
	var active atomic.Int32
	client := &Client{URL: brokerURL(listener), ClientID: "suscriptor"}
	client.Subscribe("casa/+/estado", func(message Message) {
		if active.Add(1) != 1 {
			t.Error("handlers ejecutados en paralelo")
		}
		delivered <- message
		active.Add(-1)
		if len(delivered) == 2 {
			cancel()
		}
	})
	if err := client.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first, second := receive(t, delivered), receive(t, delivered)
	if first.Topic != "casa/cocina/estado" || string(first.Payload) != "uno" || first.Retain {
		t.Fatalf("primer mensaje = %#v", first)
	}
	if second.Topic != "casa/salón/estado" || string(second.Payload) != "dos" || !second.Retain {
		t.Fatalf("segundo mensaje = %#v", second)
	}
}

func TestSilenceAfterPingReconnects(t *testing.T) {
	oldRetryDelay := retryDelay
	retryDelay = func(int, bool) time.Duration { return 0 }
	t.Cleanup(func() { retryDelay = oldRetryDelay })

	listener := listenBroker(t)
	connections := make(chan byte, 2)
	go func() {
		for number := byte(1); number <= 2; number++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reader := bufio.NewReader(conn)
			_, _ = readPacket(reader)
			_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
			connections <- number
			if number == 1 {
				ping, err := readPacket(reader)
				if err != nil || ping.header != 0xc0 {
					_ = conn.Close()
					return
				}
				_, _ = readPacket(reader)
			}
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{URL: brokerURL(listener), ClientID: "keepalive", KeepAlive: 40 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	if first := receive(t, connections); first != 1 {
		t.Fatalf("primera conexión = %d", first)
	}
	if second := receive(t, connections); second != 2 {
		t.Fatalf("segunda conexión = %d", second)
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestReconnectResubscribesAndCallsOnConnectAgain(t *testing.T) {
	oldRetryDelay := retryDelay
	retryDelay = func(int, bool) time.Duration { return 0 }
	t.Cleanup(func() { retryDelay = oldRetryDelay })

	listener := listenBroker(t)
	packetIDs := make(chan uint16, 2)
	go func() {
		for number := 1; number <= 2; number++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reader := bufio.NewReader(conn)
			_, _ = readPacket(reader)
			_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
			subscribe, err := readPacket(reader)
			if err != nil || subscribe.header != 0x82 || len(subscribe.body) < 2 {
				_ = conn.Close()
				return
			}
			id := binary.BigEndian.Uint16(subscribe.body)
			packetIDs <- id
			_, _ = conn.Write([]byte{0x90, 0x03, byte(id >> 8), byte(id), 0x00})
			if number == 2 {
				_, _ = conn.Write(serverPublish("casa/estado", "reconectado", false))
			}
			_ = conn.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	var connected atomic.Int32
	delivered := make(chan Message, 1)
	client := &Client{
		URL:       brokerURL(listener),
		ClientID:  "reconexión",
		OnConnect: func() { connected.Add(1) },
	}
	client.Subscribe("casa/#", func(message Message) {
		delivered <- message
		cancel()
	})
	if err := client.Run(ctx); err != nil {
		t.Fatal(err)
	}
	firstID, secondID := receive(t, packetIDs), receive(t, packetIDs)
	if firstID == 0 || secondID == 0 || firstID == secondID {
		t.Fatalf("packet IDs = %d, %d", firstID, secondID)
	}
	if connected.Load() != 2 {
		t.Fatalf("OnConnect llamado %d veces; quiere 2", connected.Load())
	}
	if message := receive(t, delivered); string(message.Payload) != "reconectado" {
		t.Fatalf("mensaje tras reconexión = %#v", message)
	}
}

func TestInvalidServerPacketsCauseReconnect(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"remaining length de cinco bytes", []byte{0x30, 0x80, 0x80, 0x80, 0x80, 0x00}},
		{"paquete mayor de 64 KiB", []byte{0x30, 0x80, 0x80, 0x04}},
		{"PUBLISH QoS 1", []byte{0x32, 0x05, 0x00, 0x01, 'a', 0x00, 0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldRetryDelay := retryDelay
			retryDelay = func(int, bool) time.Duration { return 0 }
			t.Cleanup(func() { retryDelay = oldRetryDelay })

			listener := listenBroker(t)
			go func() {
				for number := 1; number <= 2; number++ {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					_, _ = readPacket(bufio.NewReader(conn))
					_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
					if number == 1 {
						_, _ = conn.Write(tt.data)
					}
					_ = conn.Close()
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			var connections atomic.Int32
			client := &Client{
				URL:      brokerURL(listener),
				ClientID: "paquete-inválido",
				OnConnect: func() {
					if connections.Add(1) == 2 {
						cancel()
					}
				},
			}
			if err := client.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if connections.Load() != 2 {
				t.Fatalf("conexiones = %d; quiere 2", connections.Load())
			}
		})
	}
}

func listenBroker(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skipf("el sandbox no permite abrir sockets: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func brokerURL(listener net.Listener) string {
	return "mqtt://" + listener.Addr().String()
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timeout esperando dato del broker")
		var zero T
		return zero
	}
}

func takeString(body []byte) (string, []byte, error) {
	if len(body) < 2 {
		return "", nil, io.ErrUnexpectedEOF
	}
	n := int(binary.BigEndian.Uint16(body))
	if len(body) < n+2 {
		return "", nil, io.ErrUnexpectedEOF
	}
	return string(body[2 : 2+n]), body[2+n:], nil
}

func takeStrings(t *testing.T, body []byte, count int) []string {
	t.Helper()
	result := make([]string, 0, count)
	for range count {
		value, rest, err := takeString(body)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
		body = rest
	}
	return result
}

func serverPublish(topic, payload string, retain bool) []byte {
	body := appendString(nil, topic)
	body = append(body, payload...)
	header := byte(0x30)
	if retain {
		header++
	}
	return encodePacket(header, body)
}

func TestPingIsSentWhilePublishingContinuously(t *testing.T) {
	listener := listenBroker(t)
	pinged := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = readPacket(reader)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		for {
			next, err := readPacket(reader)
			if err != nil {
				return
			}
			if next.header == 0xc0 {
				_, _ = conn.Write([]byte{0xd0, 0x00})
				select {
				case pinged <- struct{}{}:
				default:
				}
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{URL: brokerURL(listener), ClientID: "publicador", KeepAlive: 60 * time.Millisecond}
	go func() { _ = client.Run(ctx) }()
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = client.Publish("casa/estado", []byte("x"), false)
			}
		}
	}()
	// Sin PINGREQ, un broker que no envía nada dispararía el deadline de lectura.
	receive(t, pinged)
}

func TestSubackFailureIsLogged(t *testing.T) {
	oldOutput := logx.Out
	var output bytes.Buffer
	logx.Out = &output
	t.Cleanup(func() { logx.Out = oldOutput })

	client := &Client{}
	if err := client.handleIncoming(packet{header: 0x90, body: []byte{0x00, 0x01, 0x80}}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "suscripción rechazada") {
		t.Fatalf("registro = %q; quiere aviso de suscripción rechazada", output.String())
	}
}

func TestHandlersReceiveIndependentPayloads(t *testing.T) {
	client := &Client{}
	var second string
	subscriptions := []subscription{
		{filter: "casa/#", handler: func(message Message) { message.Payload[0] = 'X' }},
		{filter: "casa/estado", handler: func(message Message) { second = string(message.Payload) }},
	}
	if err := client.handleIncoming(packet{header: 0x30, body: serverPublish("casa/estado", "online", false)[2:]}, subscriptions); err != nil {
		t.Fatal(err)
	}
	if second != "online" {
		t.Fatalf("segundo handler recibió %q; quiere online", second)
	}
}
