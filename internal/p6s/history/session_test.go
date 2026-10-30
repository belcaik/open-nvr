package history

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/belcaik/open-nvr/internal/p6s"
)

func capturedMultiTypeRequest() HistoryStreamCreateMultiTypeRequest {
	return HistoryStreamCreateMultiTypeRequest{
		DiskGroup: 1, Channel: 0, EnableEndTime: 0, StreamType: 63,
		TransportType: 1, StreamMediaType: 0, TaskType: 1001, Version: -1,
		BeginTime: DateTime{Year: 2026, Month: 10, Day: 28},
		EndTime:   DateTime{Year: 2026, Month: 10, Day: 28, Hour: 23, Minute: 59, Second: 59},
	}
}

func TestSessionCreateBindsCapturedMediaFlag(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()

	serverErr := make(chan error, 1)
	go func() {
		command, err := p6s.ReadCommand(commandServer)
		if err != nil {
			serverErr <- err
			return
		}
		if string(command.XML) != capturedWireXML(`<Command ID="185"><HistoryStreamCreateMultiType Version="-1"><HistoryStreamPara DiskGroup="1" Channel="0" EnableEndTime="0" StreamType="63" TransportType="1" StreamMediaType="0" TaskType="1001"><BeginTime Year="2026" Month="10" Day="28" Hour="0" Minute="0" Second="0"></BeginTime><EndTime Year="2026" Month="10" Day="28" Hour="23" Minute="59" Second="59"></EndTime></HistoryStreamPara></HistoryStreamCreateMultiType></Command>`) {
			serverErr <- &testError{"unexpected create XML"}
			return
		}
		if err := p6s.WriteCommand(commandServer, p6s.Command{Sequence: 77, XML: []byte(`<Command ID="78"><HeartBeatNotice UserData="0" HeartBeatMessage=""></HeartBeatNotice></Command>`)}); err != nil {
			serverErr <- err
			return
		}
		heartbeat, err := p6s.ReadCommand(commandServer)
		if err != nil {
			serverErr <- err
			return
		}
		if heartbeat.Sequence != 77 || string(heartbeat.XML) != capturedWireXML(`<Command ID="79"><HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" NetHistoryDataFlow="0"></HeartBeatNoticeReply></Command>`) {
			serverErr <- &testError{"unexpected heartbeat reply"}
			return
		}
		err = p6s.WriteCommand(commandServer, p6s.Command{Sequence: command.Sequence, XML: []byte(capturedWireXML(`<Command ID="125"><HistoryStreamCreateReply CmdReply="0" Version="28758304" MediaFlag="516516436"></HistoryStreamCreateReply></Command>`))})
		if err != nil {
			serverErr <- err
			return
		}
		link, err := p6s.ReadMediaFrame(mediaServer)
		if err != nil {
			serverErr <- err
			return
		}
		if len(link.Extension) != 4 || binary.BigEndian.Uint32(link.Extension) != 516516436 || len(link.Payload) != 0 {
			serverErr <- &testError{"unexpected media link"}
			return
		}
		serverErr <- nil
	}()

	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Create(context.Background(), capturedMultiTypeRequest()); err != nil {
		t.Fatal(err)
	}
	if session.MediaFlag() != 516516436 {
		t.Fatalf("MediaFlag = %d", session.MediaFlag())
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestSessionPlaybackLifecycle(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()

	commandErr := make(chan error, 1)
	go func() {
		steps := []struct {
			wantID  uint16
			wantXML string
			reply   string
		}{
			{185, `<Command ID="185"><HistoryStreamCreateMultiType Version="-1"><HistoryStreamPara DiskGroup="1" Channel="0" EnableEndTime="0" StreamType="63" TransportType="1" StreamMediaType="0" TaskType="1001"><BeginTime Year="2026" Month="10" Day="28" Hour="0" Minute="0" Second="0"></BeginTime><EndTime Year="2026" Month="10" Day="28" Hour="23" Minute="59" Second="59"></EndTime></HistoryStreamPara></HistoryStreamCreateMultiType></Command>`, `<Command ID="125"><HistoryStreamCreateReply CmdReply="0" Version="28758304" MediaFlag="516516436"></HistoryStreamCreateReply></Command>`},
			{130, `<Command ID="130"><HistoryStreamCtl Operation="1" MediaFlag="516516436" Speed="0"></HistoryStreamCtl></Command>`, `<Command ID="131"><HistoryStreamCtlReply Operation="40581976" CmdReply="0" Version="40581688"></HistoryStreamCtlReply></Command>`},
			{128, `<Command ID="128"><HistoryStreamPos Operation="2" MediaFlag="516516436" StreamFlag="1"><PosTime Year="2026" Month="10" Day="28" Hour="0" Minute="0" Second="0"></PosTime></HistoryStreamPos></Command>`, `<Command ID="129"><HistoryStreamPosReply CmdReply="0" Version="1482436176"></HistoryStreamPosReply></Command>`},
			{130, `<Command ID="130"><HistoryStreamCtl Operation="3" MediaFlag="516516436" Speed="0"></HistoryStreamCtl></Command>`, `<Command ID="131"><HistoryStreamCtlReply Operation="23885064" CmdReply="0" Version="39788536"></HistoryStreamCtlReply></Command>`},
			{130, `<Command ID="130"><HistoryStreamCtl Operation="2" MediaFlag="516516436" Speed="0"></HistoryStreamCtl></Command>`, `<Command ID="131"><HistoryStreamCtlReply Operation="40248696" CmdReply="0" Version="39247136"></HistoryStreamCtlReply></Command>`},
			{126, `<Command ID="126"><HistoryStreamDestroy MediaFlag="516516436" Version="-1"></HistoryStreamDestroy></Command>`, `<Command ID="127"><HistoryStreamDestroyReply CmdReply="0" Version="1482436176"></HistoryStreamDestroyReply></Command>`},
		}
		for _, step := range steps {
			command, err := p6s.ReadCommand(commandServer)
			if err != nil {
				commandErr <- err
				return
			}
			if string(command.XML) != capturedWireXML(step.wantXML) {
				commandErr <- fmt.Errorf("ID %d XML inesperado: %s", step.wantID, command.XML)
				return
			}
			if err := p6s.WriteCommand(commandServer, p6s.Command{Sequence: command.Sequence, XML: []byte(step.reply)}); err != nil {
				commandErr <- err
				return
			}
		}
		commandErr <- nil
	}()

	mediaErr := make(chan error, 1)
	go func() {
		link, err := p6s.ReadMediaFrame(mediaServer)
		if err != nil {
			mediaErr <- err
			return
		}
		if len(link.Extension) != 4 || binary.BigEndian.Uint32(link.Extension) != 516516436 {
			mediaErr <- &testError{"unexpected media link"}
			return
		}
		if err := p6s.WriteMediaFrame(mediaServer, p6s.MediaFrame{Extension: make([]byte, 4)}); err != nil {
			mediaErr <- err
			return
		}
		if err := p6s.WriteMediaFrame(mediaServer, p6s.MediaFrame{Extension: make([]byte, 44), Payload: []byte{0, 0, 1, 0xba}}); err != nil {
			mediaErr <- err
			return
		}
		mediaErr <- nil
	}()

	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Create(context.Background(), capturedMultiTypeRequest()); err != nil {
		t.Fatal(err)
	}
	ack, err := session.ReadMediaFrame(context.Background())
	if err != nil || len(ack.Extension) != 4 || len(ack.Payload) != 0 {
		t.Fatalf("media ack = %#v, %v", ack, err)
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	frame, err := session.ReadMediaFrame(context.Background())
	if err != nil || len(frame.Extension) != 44 || string(frame.Payload) != string([]byte{0, 0, 1, 0xba}) {
		t.Fatalf("media frame = %#v, %v", frame, err)
	}
	if err := session.Seek(context.Background(), DateTime{Year: 2026, Month: 10, Day: 28}); err != nil {
		t.Fatal(err)
	}
	if err := session.Control(context.Background(), ControlSpeed, "0"); err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.MediaFlag() != 0 {
		t.Fatalf("MediaFlag after Destroy = %d", session.MediaFlag())
	}
	if err := <-commandErr; err != nil {
		t.Fatal(err)
	}
	if err := <-mediaErr; err != nil {
		t.Fatal(err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }

func capturedWireXML(document string) string {
	return `<?xml version="1.0" encoding="GB2312" standalone="yes" ?>` + "\n" + document + "\x00"
}

func TestSessionAnswersHeartbeatWhileIdle(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()

	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result := make(chan error, 1)
	go func() {
		if err := p6s.WriteCommand(commandServer, p6s.Command{Sequence: 91, XML: []byte(`<Command ID="78"><HeartBeatNotice UserData="0"></HeartBeatNotice></Command>`)}); err != nil {
			result <- err
			return
		}
		reply, err := p6s.ReadCommand(commandServer)
		if err != nil {
			result <- err
			return
		}
		if reply.Sequence != 91 || string(reply.XML) != capturedWireXML(`<Command ID="79"><HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" NetHistoryDataFlow="0"></HeartBeatNoticeReply></Command>`) {
			result <- &testError{"unexpected idle heartbeat reply"}
			return
		}
		result <- nil
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("idle heartbeat was not answered")
	}
}

func TestSessionHeartbeatReportsAndResetsHistoryFlow(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()

	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	mediaErr := make(chan error, 1)
	go func() {
		mediaErr <- p6s.WriteMediaFrame(mediaServer, p6s.MediaFrame{Extension: make([]byte, 44), Payload: []byte{1, 2, 3, 4}})
	}()
	if _, err := session.ReadMediaFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-mediaErr; err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		for _, step := range []struct {
			sequence uint32
			flow     string
		}{{92, "4"}, {93, "0"}} {
			if err := p6s.WriteCommand(commandServer, p6s.Command{Sequence: step.sequence, XML: []byte(`<Command ID="78"><HeartBeatNotice UserData="0"></HeartBeatNotice></Command>`)}); err != nil {
				result <- err
				return
			}
			reply, err := p6s.ReadCommand(commandServer)
			if err != nil {
				result <- err
				return
			}
			want := capturedWireXML(`<Command ID="79"><HeartBeatNoticeReply CmdReply="0" NetDataFlow="0" NetHistoryDataFlow="` + step.flow + `"></HeartBeatNoticeReply></Command>`)
			if reply.Sequence != step.sequence || string(reply.XML) != want {
				result <- fmt.Errorf("heartbeat flow = %s, want %s", reply.XML, want)
				return
			}
		}
		result <- nil
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("history flow heartbeat was not answered")
	}
}

func TestSessionCancellationClosesUncertainExchange(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()

	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}
	requestRead := make(chan error, 1)
	go func() {
		_, err := p6s.ReadCommand(commandServer)
		requestRead <- err
	}()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- session.Create(ctx, capturedMultiTypeRequest()) }()
	if err := <-requestRead; err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := p6s.ReadCommand(commandServer); err == nil {
		t.Fatal("command connection remained open")
	}
	if _, err := p6s.ReadMediaFrame(mediaServer); err == nil {
		t.Fatal("media connection remained open")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func TestSessionMediaCancellationClosesPartialFrame(t *testing.T) {
	commandClient, commandServer := net.Pipe()
	mediaClient, mediaServer := net.Pipe()
	defer commandServer.Close()
	defer mediaServer.Close()
	session, err := NewSession(context.Background(), commandClient, mediaClient)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := session.ReadMediaFrame(ctx)
		result <- err
	}()
	if _, err := mediaServer.Write([]byte{0x05, 0x01, 0x11, 0x50}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadMediaFrame error = %v", err)
	}
	if err := commandServer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err == nil {
		var one [1]byte
		if _, err := commandServer.Read(one[:]); err == nil {
			t.Fatal("command connection remained open")
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("command connection remained open until timeout")
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}
