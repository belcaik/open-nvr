package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/logx"
	"github.com/belcaik/open-nvr/internal/p6s/voice"
	"github.com/belcaik/open-nvr/internal/talk"
	"github.com/coder/websocket"
)

const (
	talkProtocolPrefix = "opennvr-talk."
	talkQueueFrames    = 4
	talkMaxDuration    = 5 * time.Minute
	// talkReady avisa al navegador de que la sesión P6S ya está abierta.
	talkReady = "ready"
)

type talkClose struct {
	code   websocket.StatusCode
	reason string
}

func (e talkClose) Error() string { return e.reason }

func (s *server) talk(w http.ResponseWriter, r *http.Request) {
	channel, ok := s.channel(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	wantedProtocol := talkProtocolPrefix + s.d.Sessions.CSRFToken(sessionFrom(r))
	if !headerToken(r.Header.Values("Sec-WebSocket-Protocol"), wantedProtocol) {
		http.Error(w, "Token CSRF no válido", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{wantedProtocol},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(voice.PCM16FrameBytes)

	ctx, cancel := context.WithTimeout(context.Background(), talkMaxDuration)
	session, err := s.d.Talk.Open(ctx, channel)
	if err != nil {
		cancel()
		logx.Printf("talk canal %d: abrir voz: %v", channel, err)
		code, reason := websocket.StatusInternalError, "No se pudo abrir el micrófono"
		switch {
		// El NVR responde CmdReply=1 cuando el canal ya tiene otra sesión.
		case errors.Is(err, talk.ErrBusy) || errors.Is(err, voice.ErrCmdReply):
			code, reason = websocket.StatusTryAgainLater, "El audio de esta cámara está en uso por otra sesión"
		case errors.Is(err, voice.ErrResult):
			code, reason = websocket.StatusPolicyViolation, "La cámara rechazó la sesión de audio"
		}
		_ = conn.Close(code, reason)
		return
	}
	defer session.Close()
	if err := conn.Write(ctx, websocket.MessageText, []byte(talkReady)); err != nil {
		cancel()
		return
	}

	frames := make(chan []byte, talkQueueFrames)
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		errs <- readTalkFrames(ctx, conn, frames)
	}()
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case frame := <-frames:
				if err := session.SendPCM(ctx, frame); err != nil {
					errs <- err
					return
				}
			}
		}
	}()

	err = <-errs
	var closeErr talkClose
	switch {
	case errors.As(err, &closeErr):
		_ = conn.Close(closeErr.code, closeErr.reason)
	case errors.Is(err, context.DeadlineExceeded):
		_ = conn.Close(websocket.StatusNormalClosure, "Duración máxima alcanzada")
	case websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway:
		// El peer ya inició el cierre normal.
	default:
		logx.Printf("talk canal %d: sesión interrumpida: %v", channel, err)
		_ = conn.Close(websocket.StatusInternalError, "Se interrumpió el micrófono")
	}
	cancel()
	_ = conn.CloseNow()
	workers.Wait()
}

func readTalkFrames(ctx context.Context, conn *websocket.Conn, frames chan<- []byte) error {
	for {
		typ, frame, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary {
			return talkClose{websocket.StatusUnsupportedData, "Sólo se acepta PCM binario"}
		}
		if len(frame) != voice.PCM16FrameBytes {
			return talkClose{websocket.StatusPolicyViolation, "Frame PCM no válido"}
		}
		select {
		case frames <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func headerToken(values []string, wanted string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			if strings.TrimSpace(token) == wanted {
				return true
			}
		}
	}
	return false
}
