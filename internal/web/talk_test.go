package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s/voice"
	"github.com/belcaik/open-nvr/internal/talk"
	"github.com/coder/websocket"
)

func dialTalk(t *testing.T, e *env, channel int) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(e.h)
	se, ck := e.session(t)
	csrfProtocol := talkProtocolPrefix + e.sess.CSRFToken(se)
	header := http.Header{}
	header.Set("Cookie", ck.String())
	header.Set("Origin", server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/talk/"+itoa(channel), &websocket.DialOptions{
		HTTPHeader:   header,
		Subprotocols: []string{csrfProtocol},
	})
	if err != nil {
		if resp != nil {
			t.Fatalf("dial talk: %v (HTTP %d)", err, resp.StatusCode)
		}
		t.Fatal(err)
	}
	return conn, server
}

// readReady exige que el servidor confirme la sesión de voz antes de que el
// cliente muestre "Hablando".
func readReady(ctx context.Context, t *testing.T, conn *websocket.Conn) {
	t.Helper()
	typ, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("sin confirmación de voz: %v", err)
	}
	if typ != websocket.MessageText || string(msg) != talkReady {
		t.Fatalf("confirmación = %v %q", typ, msg)
	}
}

func TestTalkWebSocketAcceptsOnePCMFrameAndClosesSession(t *testing.T) {
	e := newEnv(t)
	conn, server := dialTalk(t, e, 2)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	readReady(ctx, t, conn)
	pcm := make([]byte, voice.PCM16FrameBytes)
	pcm[0], pcm[1] = 0x34, 0x12
	if err := conn.Write(ctx, websocket.MessageBinary, pcm); err != nil {
		t.Fatal(err)
	}

	e.talk.mu.Lock()
	session, channel := e.talk.session, e.talk.channel
	e.talk.mu.Unlock()
	select {
	case got := <-session.frames:
		if string(got) != string(pcm) {
			t.Fatal("el handler alteró el frame PCM")
		}
	case <-ctx.Done():
		t.Fatal("el frame PCM no llegó a la sesión")
	}
	if channel != 2 {
		t.Fatalf("canal = %d", channel)
	}
	if err := conn.Close(websocket.StatusNormalClosure, "fin"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.closed:
	case <-ctx.Done():
		t.Fatal("la sesión de voz no se cerró")
	}
}

func TestTalkWebSocketReportsCameraRejectingVoice(t *testing.T) {
	e := newEnv(t)
	e.talk.err = voice.ErrResult
	conn, server := dialTalk(t, e, 4)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("cierre = %d (%v); esperado %d", got, err, websocket.StatusPolicyViolation)
	}
	if !strings.Contains(err.Error(), "La cámara rechazó la sesión de audio") {
		t.Fatalf("cierre sin motivo de la cámara: %v", err)
	}
}

func TestTalkWebSocketReportsBusyChannel(t *testing.T) {
	for _, cause := range []error{talk.ErrBusy, voice.ErrCmdReply} {
		e := newEnv(t)
		e.talk.err = cause
		conn, server := dialTalk(t, e, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _, err := conn.Read(ctx)
		cancel()
		server.Close()
		if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
			t.Fatalf("%v: cierre = %d (%v)", cause, got, err)
		}
		if !strings.Contains(err.Error(), "en uso") {
			t.Fatalf("%v: cierre sin motivo de ocupado: %v", cause, err)
		}
	}
}

func TestTalkWebSocketRejectsInvalidAudio(t *testing.T) {
	tests := []struct {
		name string
		typ  websocket.MessageType
		data []byte
		want websocket.StatusCode
	}{
		{"texto", websocket.MessageText, []byte("pcm"), websocket.StatusUnsupportedData},
		{"tamaño", websocket.MessageBinary, make([]byte, voice.PCM16FrameBytes-1), websocket.StatusPolicyViolation},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			conn, server := dialTalk(t, e, 1)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			readReady(ctx, t, conn)
			if err := conn.Write(ctx, tc.typ, tc.data); err != nil {
				t.Fatal(err)
			}
			_, _, err := conn.Read(ctx)
			if got := websocket.CloseStatus(err); got != tc.want {
				t.Fatalf("cierre = %d (%v); esperado %d", got, err, tc.want)
			}
			e.talk.mu.Lock()
			session := e.talk.session
			e.talk.mu.Unlock()
			select {
			case <-session.closed:
			case <-ctx.Done():
				t.Fatal("audio inválido no cerró la sesión")
			}
		})
	}
}

func TestTalkWebSocketCancellationInterruptsSendAndCloses(t *testing.T) {
	e := newEnv(t)
	session := newFakeTalkSession()
	started := make(chan struct{})
	session.send = func(ctx context.Context, _ []byte) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	e.talk.session = session
	conn, server := dialTalk(t, e, 1)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, voice.PCM16FrameBytes)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("SendPCM no comenzó")
	}
	if err := conn.CloseNow(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.closed:
	case <-ctx.Done():
		t.Fatal("la desconexión no canceló SendPCM ni cerró voz")
	}
}

func TestTalkWebSocketRequiresSessionOriginAndCSRF(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(e.h)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/talk/1"
	se, ck := e.session(t)
	validProtocol := talkProtocolPrefix + e.sess.CSRFToken(se)

	tests := []struct {
		name       string
		cookie     string
		origin     string
		protocol   string
		wantStatus int
	}{
		{"sesión", "", server.URL, validProtocol, http.StatusUnauthorized},
		{"origen", ck.String(), "https://evil.example", validProtocol, http.StatusForbidden},
		{"csrf", ck.String(), server.URL, talkProtocolPrefix + "incorrecto", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{"Origin": {tc.origin}}
			if tc.cookie != "" {
				header.Set("Cookie", tc.cookie)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header, Subprotocols: []string{tc.protocol}})
			if conn != nil {
				conn.CloseNow()
			}
			if err == nil || resp == nil || resp.StatusCode != tc.wantStatus {
				t.Fatalf("err=%v status=%v; esperado HTTP %d", err, respStatus(resp), tc.wantStatus)
			}
		})
	}
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
