package talk_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s/voice"
	"github.com/belcaik/open-nvr/internal/talk"
)

type connectorFunc func(context.Context) (net.Conn, net.Conn, error)

func (f connectorFunc) OpenAuthenticated(ctx context.Context) (net.Conn, net.Conn, error) {
	return f(ctx)
}

func TestOpenFailureClosesAnyAuthenticatedConnection(t *testing.T) {
	command, peer := net.Pipe()
	defer peer.Close()
	adapter := talk.Adapter{
		Connector: connectorFunc(func(context.Context) (net.Conn, net.Conn, error) {
			return command, nil, nil
		}),
		OpenVoice: func(context.Context, net.Conn, net.Conn, voice.Config) (talk.VoiceSession, error) {
			return nil, errors.New("falló voz")
		},
	}
	if _, err := adapter.Open(context.Background(), 1); err == nil {
		t.Fatal("Open debía fallar")
	}
	if _, err := peer.Write([]byte{1}); err == nil {
		t.Fatal("la conexión command quedó abierta")
	}
}

type fakeVoice struct {
	frames [][]byte
	closed bool
}

func (f *fakeVoice) Send(_ context.Context, frame []byte) error {
	f.frames = append(f.frames, append([]byte(nil), frame...))
	return nil
}

func (f *fakeVoice) Close() error {
	f.closed = true
	return nil
}

func TestSessionConvertsOnePCMFrameAndClosesVoice(t *testing.T) {
	commandClient, commandPeer := net.Pipe()
	mediaClient, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	gotChannel := -1
	voiceSession := &fakeVoice{}
	adapter := talk.Adapter{
		Connector: connectorFunc(func(context.Context) (net.Conn, net.Conn, error) {
			return commandClient, mediaClient, nil
		}),
		OpenVoice: func(_ context.Context, command, media net.Conn, cfg voice.Config) (talk.VoiceSession, error) {
			if command != commandClient || media != mediaClient {
				t.Fatal("el adaptador no pasó las conexiones autenticadas")
			}
			gotChannel = cfg.Channel
			if cfg.FrameInterval != 17*time.Millisecond {
				t.Fatalf("FrameInterval = %s", cfg.FrameInterval)
			}
			return voiceSession, nil
		},
		VoiceConfig: voice.Config{Channel: 99, FrameInterval: 17 * time.Millisecond},
	}

	session, err := adapter.Open(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SendPCM(context.Background(), make([]byte, voice.PCM16FrameBytes)); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	if gotChannel != 2 {
		t.Fatalf("canal P6S = %d; esperado 2", gotChannel)
	}
	if len(voiceSession.frames) != 1 || len(voiceSession.frames[0]) != voice.PCMUFrameBytes {
		t.Fatalf("frames PCMU: %#v", voiceSession.frames)
	}
	for i, sample := range voiceSession.frames[0] {
		if sample != 0xff {
			t.Fatalf("muestra PCMU %d = %#x; esperado 0xff", i, sample)
		}
	}
	if !voiceSession.closed {
		t.Fatal("Close no cerró la sesión de voz")
	}
}
