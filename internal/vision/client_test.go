package vision

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestClientSetEnabledConnectChangeAndReconnect(t *testing.T) {
	oldBase, oldMax := clientReconnectBase, clientReconnectMax
	clientReconnectBase, clientReconnectMax = 5*time.Millisecond, 10*time.Millisecond
	defer func() { clientReconnectBase, clientReconnectMax = oldBase, oldMax }()

	messages := make(chan []int, 4)
	var connections atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		n := connections.Add(1)
		go func() {
			defer conn.Close(websocket.StatusNormalClosure, "reconectar")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			reads := 1
			if n == 1 {
				reads = 2
			}
			for range reads {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var message struct {
					Type    string `json:"type"`
					Enabled []int  `json:"enabled"`
				}
				if json.Unmarshal(data, &message) == nil && message.Type == "cameras" {
					messages <- message.Enabled
				}
			}
		}()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := NewClientWithHTTPClient("ws://detector.local/detections", &http.Client{Transport: memoryWebSocketTransport{handler: handler}})
	client.SetEnabled([]int{3, 1, 3})
	frames, _ := client.Start(ctx)
	wantMessage(t, messages, []int{1, 3})
	client.SetEnabled([]int{2, 1})
	wantMessage(t, messages, []int{1, 2})
	wantMessage(t, messages, []int{1, 2})
	cancel()
	for range frames {
	}
}

func wantMessage(t *testing.T, messages <-chan []int, want []int) {
	t.Helper()
	select {
	case got := <-messages:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("enabled = %v, quiero %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout esperando enabled %v", want)
	}
}

func receiveFrame(t *testing.T, frames <-chan Frame) Frame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(3 * time.Second):
		t.Fatal("timeout esperando frame")
		return Frame{}
	}
}

func TestClientReceivesSurvivesInvalidAndReconnects(t *testing.T) {
	oldBase, oldMax := clientReconnectBase, clientReconnectMax
	clientReconnectBase, clientReconnectMax = 10*time.Millisecond, 40*time.Millisecond
	defer func() { clientReconnectBase, clientReconnectMax = oldBase, oldMax }()

	var connections atomic.Int32
	var available atomic.Bool
	available.Store(true)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			http.Error(w, "cerrado", http.StatusServiceUnavailable)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		n := connections.Add(1)
		go func() {
			defer conn.Close(websocket.StatusNormalClosure, "reinicio")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if n == 1 {
				conn.Write(ctx, websocket.MessageText, []byte(`{"type":"detections","cam":1,"seq":1,"ts":"2026-11-03T15:30:01Z","w":800,"h":448,"tracks":[]}`))
				conn.Write(ctx, websocket.MessageText, []byte(`{"type":"detections","cam":0}`))
				conn.Write(ctx, websocket.MessageText, []byte(`{"type":"detections","cam":1,"seq":2,"ts":"2026-11-03T15:30:02Z","w":800,"h":448,"tracks":[]}`))
				available.Store(false)
				return
			}
			conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"detections","cam":1,"seq":%d,"ts":"2026-11-03T15:30:03Z","w":800,"h":448,"tracks":[]}`, n+1)))
			conn.Read(ctx)
		}()
	})
	url := "ws://detector.local/detections"
	transport := memoryWebSocketTransport{handler: handler}

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClientWithHTTPClient(url, &http.Client{Transport: transport})
	frames, _ := client.Start(ctx)
	if first := receiveFrame(t, frames); first.Seq != 1 {
		t.Fatalf("primer seq = %d", first.Seq)
	}
	if second := receiveFrame(t, frames); second.Seq != 2 {
		t.Fatalf("segundo seq = %d; el mensaje inválido cortó la conexión", second.Seq)
	}
	// El endpoint permanece caído durante varios intentos y después vuelve.
	time.Sleep(35 * time.Millisecond)
	available.Store(true)
	if third := receiveFrame(t, frames); third.Seq < 3 {
		t.Fatalf("frame tras reconectar = %+v", third)
	}
	if connections.Load() < 2 {
		t.Fatalf("conexiones = %d, quiero al menos 2", connections.Load())
	}
	if !client.Connected() {
		t.Fatalf("estado = %s, quiero connected", client.State())
	}
	cancel()
	for range frames {
	}
	if client.State() != StateDisconnected {
		t.Fatalf("estado final = %s", client.State())
	}
}

// memoryWebSocketTransport ejecuta un handler httptest sobre net.Pipe.
type memoryWebSocketTransport struct {
	handler http.Handler
}

func (transport memoryWebSocketTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clientConn, serverConn := net.Pipe()
	recorder := httptest.NewRecorder()
	hijacker := memoryHijacker{ResponseRecorder: recorder, conn: serverConn}
	transport.handler.ServeHTTP(hijacker, request)
	response := recorder.Result()
	if response.StatusCode == http.StatusSwitchingProtocols {
		response.Body = clientConn
	} else {
		clientConn.Close()
		serverConn.Close()
	}
	return response, nil
}

type memoryHijacker struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (h memoryHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}
