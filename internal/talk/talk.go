// Package talk adapta conexiones P6S autenticadas a sesiones cortas de
// micrófono. Las credenciales permanecen en el conector inyectado.
package talk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s/voice"
)

// ErrBusy indica que el canal sigue ocupado por otra sesión de este proceso.
var ErrBusy = errors.New("talk: canal de audio ocupado")

// defaultBusyWait cubre el Stop/Destroy de la sesión anterior, que el NVR
// confirma en torno a un segundo después.
const defaultBusyWait = 3 * time.Second

// AuthenticatedConnector abre los canales command y media después de hacer
// el login P6S. Su implementación es la única capa que necesita secretos.
type AuthenticatedConnector interface {
	OpenAuthenticated(ctx context.Context) (command, media net.Conn, err error)
}

// VoiceSession es el límite mínimo que Adapter necesita de voice.Session.
type VoiceSession interface {
	Send(ctx context.Context, pcmu []byte) error
	Close() error
}

// VoiceOpener permite probar el adaptador sin una cámara.
type VoiceOpener func(context.Context, net.Conn, net.Conn, voice.Config) (VoiceSession, error)

// Adapter abre una sesión de voz sobre un transporte ya autenticado.
type Adapter struct {
	Connector AuthenticatedConnector
	OpenVoice VoiceOpener
	// VoiceConfig permite inyectar la extensión opaca capturada y límites.
	// Open siempre sustituye Channel por el canal solicitado.
	VoiceConfig voice.Config
	// BusyWait limita la espera a que se libere el canal; 0 usa el valor
	// por defecto.
	BusyWait time.Duration

	mu    sync.Mutex
	slots map[int]chan struct{}
}

// New devuelve el adaptador de producción para el conector compartido.
func New(connector AuthenticatedConnector) *Adapter {
	return &Adapter{Connector: connector, OpenVoice: func(ctx context.Context, command, media net.Conn, cfg voice.Config) (VoiceSession, error) {
		return voice.Open(ctx, command, media, cfg)
	}}
}

// Session recibe exclusivamente bloques PCM S16LE mono de 40 ms a 8 kHz.
type Session interface {
	SendPCM(ctx context.Context, pcm []byte) error
	Close() error
}

// Opener abre una sesión independiente por cliente web.
type Opener interface {
	Open(ctx context.Context, channel int) (Session, error)
}

type session struct {
	voice   VoiceSession
	release func()
	once    sync.Once
	err     error
}

// Open convierte el canal web base 1 al canal P6S base 0. El NVR sólo
// admite una sesión de voz por canal, así que Open espera a que termine el
// cierre de la anterior en lugar de chocar con ella.
func (a *Adapter) Open(ctx context.Context, channel int) (Session, error) {
	if ctx == nil {
		return nil, errors.New("talk: contexto nulo")
	}
	if channel < 1 {
		return nil, fmt.Errorf("talk: canal no válido: %d", channel)
	}
	if a.Connector == nil || a.OpenVoice == nil {
		return nil, errors.New("talk: adaptador incompleto")
	}
	release, err := a.acquire(ctx, channel)
	if err != nil {
		return nil, err
	}
	command, media, err := a.Connector.OpenAuthenticated(ctx)
	if err != nil {
		release()
		return nil, fmt.Errorf("talk: abrir transporte autenticado: %w", err)
	}
	cfg := a.VoiceConfig
	// VoiceStreamCreate usa el ChannelID del NVR (base 1), no el índice base 0
	// de RealStream: con channel-1 la voz sonaba en la cámara anterior (T102).
	cfg.Channel = channel
	opened, err := a.OpenVoice(ctx, command, media, cfg)
	if err != nil {
		closeConn(command)
		closeConn(media)
		release()
		return nil, fmt.Errorf("talk: abrir voz: %w", err)
	}
	return &session{voice: opened, release: release}, nil
}

func (a *Adapter) acquire(ctx context.Context, channel int) (func(), error) {
	a.mu.Lock()
	if a.slots == nil {
		a.slots = make(map[int]chan struct{})
	}
	slot, ok := a.slots[channel]
	if !ok {
		slot = make(chan struct{}, 1)
		a.slots[channel] = slot
	}
	a.mu.Unlock()

	wait := a.BusyWait
	if wait <= 0 {
		wait = defaultBusyWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SendPCM valida, convierte a G.711 mu-law y entrega exactamente un frame.
func (s *session) SendPCM(ctx context.Context, pcm []byte) error {
	if len(pcm) != voice.PCM16FrameBytes {
		return fmt.Errorf("talk: PCM S16LE debe medir %d bytes; hay %d", voice.PCM16FrameBytes, len(pcm))
	}
	pcmu, err := voice.EncodePCMU(pcm)
	if err != nil {
		return err
	}
	return s.voice.Send(ctx, pcmu)
}

// Close provoca el Stop/Destroy implementado por voice.Session y libera el
// canal sólo cuando el NVR ya lo confirmó.
func (s *session) Close() error {
	s.once.Do(func() {
		s.err = s.voice.Close()
		s.release()
	})
	return s.err
}

func closeConn(conn net.Conn) {
	if conn != nil {
		_ = conn.Close()
	}
}
