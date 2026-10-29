package voice

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
)

const (
	capturedMediaFlag         = uint32(2003281556)
	capturedAudioExtensionHex = "0000000000000000000000000000000000000000000000000000000000000003000000000000000000000000"
)

func TestCapturedAudioExtensionFixture(t *testing.T) {
	raw, err := os.ReadFile(capturedExtensionPath(t))
	if err != nil {
		t.Fatal(err)
	}
	want, err := hex.DecodeString(capturedAudioExtensionHex)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatalf("fixture = %x, quiero %x", raw, want)
	}
	if _, err := ParseAudioExtension(raw); err != nil {
		t.Fatal(err)
	}
	if evidence, err := os.ReadFile(filepath.Join("..", "..", "..", "captures", "p6svoice", "client-audio-extension.bin")); err == nil && !bytes.Equal(evidence, raw) {
		t.Fatal("captures/p6svoice y testdata no coinciden")
	}
}

func TestOpenSendsCreateAndRejectsBadCmdReply(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			got, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			if got.Sequence != 0 {
				return errors.New("secuencia de Create")
			}
			parsed, err := ParseVoiceStreamCreateRequest(got.XML)
			if err != nil {
				return err
			}
			if parsed.Channel != 0 || parsed.VoiceMode != 2 || parsed.TransferMode != 1 || parsed.StreamType != 0 {
				return errors.New("atributos de Create")
			}
			want, err := (VoiceStreamCreateRequest{
				Channel: 0, VoiceMode: 2, TransferMode: 1, StreamType: 0,
			}).Marshal()
			if err != nil {
				return err
			}
			if !bytes.Equal(got.XML, want.Payload) {
				return errors.New("XML de Create")
			}
			reply, err := (VoiceStreamCreateReply{MediaFlag: capturedMediaFlag, CmdReply: 2}).Marshal()
			if err != nil {
				return err
			}
			return p6s.WriteCommand(commandPeer, p6s.Command{Sequence: got.Sequence, XML: reply.Payload})
		}()
	}()

	_, err := Open(context.Background(), command, media, Config{
		Extension: mustExtension(t),
	})
	if !errors.Is(err, ErrCmdReply) {
		t.Fatalf("error = %v, quiero ErrCmdReply", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestOpenAnswersHeartbeatBeforeCreateReply(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	const hbSeq = uint32(0x10203040)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			got, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			if err := writeHeartbeat(commandPeer, hbSeq); err != nil {
				return err
			}
			hb, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			if hb.Sequence != hbSeq {
				return fmt.Errorf("secuencia latido = %d", hb.Sequence)
			}
			if err := checkHeartbeatReply(hb.XML); err != nil {
				return err
			}
			reply, err := (VoiceStreamCreateReply{MediaFlag: capturedMediaFlag, CmdReply: 0}).Marshal()
			if err != nil {
				return err
			}
			if err := p6s.WriteCommand(commandPeer, p6s.Command{Sequence: got.Sequence, XML: reply.Payload}); err != nil {
				return err
			}
			var encoded bytes.Buffer
			if err := p6s.WriteMediaFrame(&encoded, p6s.MediaLinkFrame(capturedMediaFlag)); err != nil {
				return err
			}
			link := make([]byte, encoded.Len())
			if _, err := io.ReadFull(mediaPeer, link); err != nil {
				return err
			}
			start, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			startReply, err := (VoiceStreamStartReply{Result: 0}).Marshal()
			if err != nil {
				return err
			}
			if err := p6s.WriteCommand(commandPeer, p6s.Command{Sequence: start.Sequence, XML: startReply.Payload}); err != nil {
				return err
			}
			return nil
		}()
	}()

	session, err := Open(context.Background(), command, media, Config{Extension: mustExtension(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer session.closeConns()
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestOpenWritesMediaLinkAndStart(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()

	session, err := Open(context.Background(), command, media, Config{
		Extension: mustExtension(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.closeConns()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Open no envió la trama de enlace ni Start")
	}
}

func TestSendAnswersHeartbeatBetweenFrames(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	handshake := make(chan error, 1)
	go func() {
		handshake <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()
	session, err := Open(context.Background(), command, media, Config{
		Extension: mustExtension(t),
		Sleep:     func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}

	const hbSeq = uint32(0xAABBCCDD)
	hbErr := make(chan error, 1)
	go func() {
		if err := writeHeartbeat(commandPeer, hbSeq); err != nil {
			hbErr <- err
			return
		}
		hb, err := p6s.ReadCommand(commandPeer)
		if err != nil {
			hbErr <- err
			return
		}
		if hb.Sequence != hbSeq {
			hbErr <- fmt.Errorf("secuencia latido = %d", hb.Sequence)
			return
		}
		hbErr <- checkHeartbeatReply(hb.XML)
	}()

	silence := bytes.Repeat([]byte{0xFF}, PCMUFrameBytes)
	frameErr := make(chan error, 1)
	go func() {
		_, err := p6s.ReadMediaFrame(mediaPeer)
		frameErr <- err
	}()
	if err := session.Send(context.Background(), silence); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-hbErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("no se respondió el latido durante Send")
	}
	if err := <-frameErr; err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() {
		closed <- serveStopDestroy(commandPeer, capturedMediaFlag)
	}()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestOpenTimesOutWaitingForCreateReply(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	go func() {
		_, _ = p6s.ReadCommand(commandPeer)
	}()
	_, err := Open(context.Background(), command, media, Config{
		Extension:      mustExtension(t),
		CommandTimeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open = %v, quiero DeadlineExceeded", err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestOpenCancelsWaitForCreateReply(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	readCreate := make(chan struct{})
	go func() {
		_, _ = p6s.ReadCommand(commandPeer)
		close(readCreate)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Open(ctx, command, media, Config{Extension: mustExtension(t)})
		done <- err
	}()
	<-readCreate
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Open = %v, quiero context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Open no se canceló")
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestSendWritesExactPCMUFramesWithPacing(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()

	var slept []time.Duration
	session, err := Open(context.Background(), command, media, Config{
		Extension: mustExtension(t),
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.closeConns()
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}

	silence := bytes.Repeat([]byte{0xFF}, PCMUFrameBytes)
	if err := session.Send(context.Background(), silence[:319]); !errors.Is(err, ErrPCMULength) {
		t.Fatalf("payload corto: %v", err)
	}

	frames := make(chan []byte, 2)
	readErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			var buf bytes.Buffer
			frame, err := p6s.ReadMediaFrame(io.TeeReader(mediaPeer, &buf))
			if err != nil {
				readErr <- err
				return
			}
			if err := ValidatePCMUPayload(frame.Payload); err != nil {
				readErr <- err
				return
			}
			if !bytes.Equal(frame.Extension, mustExtension(t).Bytes()) {
				readErr <- fmt.Errorf("extensión = %x", frame.Extension)
				return
			}
			frames <- buf.Bytes()
		}
		readErr <- nil
	}()

	if err := session.Send(context.Background(), silence); err != nil {
		t.Fatal(err)
	}
	if err := session.Send(context.Background(), silence); err != nil {
		t.Fatal(err)
	}
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}

	wantFrame := goldenAudioFrame(t, silence)
	if !bytes.Equal(<-frames, wantFrame) || !bytes.Equal(<-frames, wantFrame) {
		t.Fatal("trama de audio distinta del golden")
	}
	if len(slept) != 1 || slept[0] != FrameDuration {
		t.Fatalf("pausas = %v, quiero un intervalo de %s", slept, FrameDuration)
	}
}

func TestSendSerializesConcurrentFrames(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()
	session, err := Open(context.Background(), command, media, Config{
		Extension: mustExtension(t), FrameInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.closeConns()
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}

	readErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			if _, err := p6s.ReadMediaFrame(mediaPeer); err != nil {
				readErr <- err
				return
			}
		}
		readErr <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sendErr := make(chan error, 2)
	silence := bytes.Repeat([]byte{0xff}, PCMUFrameBytes)
	for i := 0; i < 2; i++ {
		go func() { sendErr <- session.Send(ctx, silence) }()
	}
	if err := <-readErr; err != nil {
		_ = mediaPeer.Close()
		t.Fatalf("frames concurrentes: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := <-sendErr; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloseSendsStopThenDestroy(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	handshake := make(chan error, 1)
	go func() {
		handshake <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()
	session, err := Open(context.Background(), command, media, Config{Extension: mustExtension(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() {
		closed <- serveStopDestroy(commandPeer, capturedMediaFlag)
	}()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestCloseDestroysAfterStopError(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	handshake := make(chan error, 1)
	go func() {
		handshake <- serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag)
	}()
	session, err := Open(context.Background(), command, media, Config{Extension: mustExtension(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-handshake; err != nil {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() {
		closed <- serveStopDestroyResult(commandPeer, capturedMediaFlag, 5, 1)
	}()
	err = session.Close()
	if !errors.Is(err, ErrResult) {
		t.Fatalf("Close = %v, quiero ErrResult por Stop", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestRunSendsSilenceAndCloses(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	silence := bytes.Repeat([]byte{0xFF}, PCMUFrameBytes)
	wantFrame := goldenAudioFrame(t, silence)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			if err := serveCreateLinkStart(commandPeer, mediaPeer, capturedMediaFlag); err != nil {
				return err
			}
			for i := 0; i < 2; i++ {
				var buf bytes.Buffer
				frame, err := p6s.ReadMediaFrame(io.TeeReader(mediaPeer, &buf))
				if err != nil {
					return err
				}
				if err := ValidatePCMUPayload(frame.Payload); err != nil {
					return err
				}
				if !bytes.Equal(buf.Bytes(), wantFrame) {
					return fmt.Errorf("trama %d", i)
				}
			}
			return serveStopDestroy(commandPeer, capturedMediaFlag)
		}()
	}()

	err := Run(context.Background(), command, media, Config{
		Extension: mustExtension(t),
		Sleep:     func(context.Context, time.Duration) error { return nil },
	}, [][]byte{silence, silence})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func TestOpenStartFailureStillDestroysAndCloses(t *testing.T) {
	command, commandPeer := net.Pipe()
	media, mediaPeer := net.Pipe()
	defer commandPeer.Close()
	defer mediaPeer.Close()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- func() error {
			got, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			reply, err := (VoiceStreamCreateReply{MediaFlag: capturedMediaFlag, CmdReply: 0}).Marshal()
			if err != nil {
				return err
			}
			if err := p6s.WriteCommand(commandPeer, p6s.Command{Sequence: got.Sequence, XML: reply.Payload}); err != nil {
				return err
			}
			if _, err := p6s.ReadMediaFrame(mediaPeer); err != nil {
				return err
			}
			start, err := p6s.ReadCommand(commandPeer)
			if err != nil {
				return err
			}
			startReply, err := (VoiceStreamStartReply{Result: -1}).Marshal()
			if err != nil {
				return err
			}
			if err := p6s.WriteCommand(commandPeer, p6s.Command{Sequence: start.Sequence, XML: startReply.Payload}); err != nil {
				return err
			}
			return serveStopDestroy(commandPeer, capturedMediaFlag)
		}()
	}()

	_, err := Open(context.Background(), command, media, Config{Extension: mustExtension(t)})
	if !errors.Is(err, ErrResult) {
		t.Fatalf("Open = %v, quiero ErrResult", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	assertClosed(t, commandPeer)
	assertClosed(t, mediaPeer)
}

func serveStopDestroy(command net.Conn, flag uint32) error {
	return serveStopDestroyResult(command, flag, 0, 1)
}

func serveStopDestroyResult(command net.Conn, flag uint32, stopResult, destroyReply int) error {
	stop, err := p6s.ReadCommand(command)
	if err != nil {
		return err
	}
	gotStop, err := ParseVoiceStreamStopRequest(stop.XML)
	if err != nil {
		return err
	}
	if gotStop.MediaSession != flag {
		return fmt.Errorf("Stop MediaSession = %d", gotStop.MediaSession)
	}
	if bytes.Contains(stop.XML, []byte("MediaFlag")) {
		return errors.New("Stop usa MediaFlag")
	}
	wantStop, err := (VoiceStreamStopRequest{MediaSession: flag}).Marshal()
	if err != nil {
		return err
	}
	if !bytes.Equal(stop.XML, wantStop.Payload) {
		return errors.New("XML de Stop")
	}
	stopReply, err := (VoiceStreamStopReply{Result: stopResult}).Marshal()
	if err != nil {
		return err
	}
	if err := p6s.WriteCommand(command, p6s.Command{Sequence: stop.Sequence, XML: stopReply.Payload}); err != nil {
		return err
	}

	destroy, err := p6s.ReadCommand(command)
	if err != nil {
		return err
	}
	if destroy.Sequence != stop.Sequence+1 {
		return fmt.Errorf("secuencia Destroy = %d", destroy.Sequence)
	}
	gotDestroy, err := ParseVoiceStreamDestroyRequest(destroy.XML)
	if err != nil {
		return err
	}
	if gotDestroy.MediaFlag != flag {
		return fmt.Errorf("Destroy MediaFlag = %d", gotDestroy.MediaFlag)
	}
	if bytes.Contains(destroy.XML, []byte("MediaSession")) {
		return errors.New("Destroy usa MediaSession")
	}
	wantDestroy, err := (VoiceStreamDestroyRequest{MediaFlag: flag}).Marshal()
	if err != nil {
		return err
	}
	if !bytes.Equal(destroy.XML, wantDestroy.Payload) {
		return errors.New("XML de Destroy")
	}
	destroyReplyXML, err := (VoiceStreamDestroyReply{CmdReply: destroyReply}).Marshal()
	if err != nil {
		return err
	}
	return p6s.WriteCommand(command, p6s.Command{Sequence: destroy.Sequence, XML: destroyReplyXML.Payload})
}

func goldenAudioFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	header, err := hex.DecodeString("05011150000010010000002c0000014000000003000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	ext, err := hex.DecodeString(capturedAudioExtensionHex)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte{}, header...)
	out = append(out, ext...)
	out = append(out, payload...)
	if len(out) != 32+44+320 {
		t.Fatalf("golden = %d bytes", len(out))
	}
	return out
}

func writeHeartbeat(conn net.Conn, sequence uint32) error {
	body := append([]byte(`<?xml version="1.0" encoding="GB2312" standalone="yes" ?><Command ID="78"><HeartBeatNotice/></Command>`), 0)
	return p6s.WriteCommand(conn, p6s.Command{Sequence: sequence, XML: body})
}

func checkHeartbeatReply(xml []byte) error {
	if !bytes.Contains(xml, []byte(`ID="79"`)) {
		return fmt.Errorf("latido ID: %s", xml)
	}
	for _, attr := range []string{`CmdReply="0"`, `NetDataFlow="0"`, `NetHistoryDataFlow="0"`} {
		if !bytes.Contains(xml, []byte(attr)) {
			return fmt.Errorf("falta %s en %s", attr, xml)
		}
	}
	return nil
}

func capturedExtensionPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "client-audio-extension.bin")
}

func mustExtension(t *testing.T) AudioExtension {
	t.Helper()
	raw, err := os.ReadFile(capturedExtensionPath(t))
	if err != nil {
		t.Fatal(err)
	}
	ext, err := ParseAudioExtension(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

func assertClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	var buf [1]byte
	_, err := conn.Read(buf[:])
	if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, io.EOF) {
		t.Fatalf("la conexión no quedó cerrada: %v", err)
	}
}

func serveCreateLinkStart(command, media net.Conn, flag uint32) error {
	got, err := p6s.ReadCommand(command)
	if err != nil {
		return err
	}
	parsed, err := ParseVoiceStreamCreateRequest(got.XML)
	if err != nil {
		return err
	}
	if parsed.Channel != 0 || parsed.VoiceMode != 2 || parsed.TransferMode != 1 || parsed.StreamType != 0 {
		return errors.New("atributos de Create")
	}
	reply, err := (VoiceStreamCreateReply{MediaFlag: flag, CmdReply: 0}).Marshal()
	if err != nil {
		return err
	}
	if err := p6s.WriteCommand(command, p6s.Command{Sequence: got.Sequence, XML: reply.Payload}); err != nil {
		return err
	}

	wantLink, err := hex.DecodeString("05011150000010010000000400000000000000030000000000000000000000007767a694")
	if err != nil {
		return err
	}
	if flag != capturedMediaFlag {
		return fmt.Errorf("esta trama de enlace golden es de MediaFlag %d", capturedMediaFlag)
	}
	link := make([]byte, len(wantLink))
	if _, err := io.ReadFull(media, link); err != nil {
		return err
	}
	if !bytes.Equal(link, wantLink) {
		return fmt.Errorf("trama de enlace = %x", link)
	}

	start, err := p6s.ReadCommand(command)
	if err != nil {
		return err
	}
	if start.Sequence != got.Sequence+1 {
		return fmt.Errorf("secuencia Start = %d", start.Sequence)
	}
	gotStart, err := ParseVoiceStreamStartRequest(start.XML)
	if err != nil {
		return err
	}
	if gotStart.MediaSession != flag {
		return fmt.Errorf("MediaSession = %d", gotStart.MediaSession)
	}
	wantStart, err := (VoiceStreamStartRequest{MediaSession: flag}).Marshal()
	if err != nil {
		return err
	}
	if !bytes.Equal(start.XML, wantStart.Payload) {
		return errors.New("XML de Start")
	}
	startReply, err := (VoiceStreamStartReply{Result: 0}).Marshal()
	if err != nil {
		return err
	}
	return p6s.WriteCommand(command, p6s.Command{Sequence: start.Sequence, XML: startReply.Payload})
}
