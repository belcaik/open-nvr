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

func pipeConnector() connectorFunc {
	return func(context.Context) (net.Conn, net.Conn, error) {
		command, commandPeer := net.Pipe()
		media, mediaPeer := net.Pipe()
		_ = commandPeer.Close()
		_ = mediaPeer.Close()
		return command, media, nil
	}
}

// El NVR admite una sesión de voz por canal y Close tarda en recibir
// Destroy: una nueva pulsación debe esperar al cierre anterior.
func TestOpenWaitsForPreviousSessionOnSameChannel(t *testing.T) {
	adapter := talk.Adapter{
		Connector: pipeConnector(),
		OpenVoice: func(context.Context, net.Conn, net.Conn, voice.Config) (talk.VoiceSession, error) {
			return &fakeVoice{}, nil
		},
	}
	first, err := adapter.Open(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := adapter.Open(context.Background(), 2)
	if err != nil {
		t.Fatalf("otro canal no debía esperar: %v", err)
	}
	defer other.Close()

	opened := make(chan error, 1)
	go func() {
		second, err := adapter.Open(context.Background(), 1)
		if err == nil {
			defer second.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("la segunda sesión no esperó al cierre: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("la segunda sesión no se abrió tras el cierre")
	}
}

func TestOpenReportsBusyChannelAfterWait(t *testing.T) {
	adapter := talk.Adapter{
		Connector: pipeConnector(),
		OpenVoice: func(context.Context, net.Conn, net.Conn, voice.Config) (talk.VoiceSession, error) {
			return &fakeVoice{}, nil
		},
		BusyWait: 20 * time.Millisecond,
	}
	first, err := adapter.Open(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := adapter.Open(context.Background(), 1); !errors.Is(err, talk.ErrBusy) {
		t.Fatalf("error = %v, quiero ErrBusy", err)
	}
}

func TestOpenFailureReleasesChannel(t *testing.T) {
	fail := true
	adapter := talk.Adapter{
		Connector: pipeConnector(),
		OpenVoice: func(context.Context, net.Conn, net.Conn, voice.Config) (talk.VoiceSession, error) {
			if fail {
				return nil, errors.New("falló voz")
			}
			return &fakeVoice{}, nil
		},
		BusyWait: 20 * time.Millisecond,
	}
	if _, err := adapter.Open(context.Background(), 1); err == nil {
		t.Fatal("Open debía fallar")
	}
	fail = false
	session, err := adapter.Open(context.Background(), 1)
	if err != nil {
		t.Fatalf("el canal quedó retenido tras el fallo: %v", err)
	}
	_ = session.Close()
}
