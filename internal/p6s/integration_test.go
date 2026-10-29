package p6s_test

import (
	"bytes"
	"testing"

	"github.com/belcaik/open-nvr/internal/p6s"
	"github.com/belcaik/open-nvr/internal/p6s/history"
	"github.com/belcaik/open-nvr/internal/p6s/voice"
)

func TestVoiceCommandThroughCoreFraming(t *testing.T) {
	message, err := (voice.VoiceStreamCreateRequest{
		Channel: 0, StreamType: 0, TransferMode: 1, VoiceMode: 2,
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := p6s.WriteCommand(&wire, p6s.Command{Sequence: 7, XML: message.Payload}); err != nil {
		t.Fatal(err)
	}
	frame, err := p6s.ReadCommand(oneByteReader{&wire})
	if err != nil {
		t.Fatal(err)
	}
	if frame.Sequence != 7 || !bytes.Equal(frame.XML, message.Payload) {
		t.Fatal("el framing no conservó el comando de voz")
	}
	if _, err := voice.ParseVoiceStreamCreateRequest(frame.XML); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryCommandThroughCoreFraming(t *testing.T) {
	message, err := history.NewHistoryStreamCreate(history.HistoryStreamCreateRequest{
		Channel: 1, DiskGroup: "0", EnableEndTime: 1, StreamType: 1,
		TransportType: "1", StreamMediaType: "0", TaskType: history.TaskPlayback, Version: "1",
		BeginTime: history.DateTime{Year: 2026, Month: 10, Day: 28, Hour: 12},
		EndTime:   history.DateTime{Year: 2026, Month: 10, Day: 28, Hour: 12, Minute: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := p6s.WriteCommand(&wire, p6s.Command{Sequence: 8, XML: message.Payload}); err != nil {
		t.Fatal(err)
	}
	frame, err := p6s.ReadCommand(oneByteReader{&wire})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.XML, message.Payload) || !bytes.Contains(frame.XML, []byte("<HistoryStreamPara>")) {
		t.Fatal("el framing no conservó el comando histórico")
	}
}

func TestPCMUAudioThroughCoreMediaFraming(t *testing.T) {
	pcmu, err := voice.EncodePCMU(make([]byte, voice.PCM16FrameBytes))
	if err != nil {
		t.Fatal(err)
	}
	extension, err := voice.ParseAudioExtension(make([]byte, voice.AudioExtensionLen))
	if err != nil {
		t.Fatal(err)
	}
	want := p6s.MediaFrame{Extension: extension.Bytes(), Payload: pcmu}
	var wire bytes.Buffer
	if err := p6s.WriteMediaFrame(&wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := p6s.ReadMediaFrame(oneByteReader{&wire})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Extension, want.Extension) || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatal("el framing no conservó el audio")
	}
	if err := voice.ValidatePCMUPayload(got.Payload); err != nil {
		t.Fatal(err)
	}
}

type oneByteReader struct{ *bytes.Buffer }

func (r oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Buffer.Read(p)
}
