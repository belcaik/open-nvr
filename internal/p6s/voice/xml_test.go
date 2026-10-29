package voice

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
)

const capturedMedia uint32 = 2003281556

func TestVoiceStreamIDsXMLAndLengths(t *testing.T) {
	create, err := (VoiceStreamCreateRequest{
		Channel:      0,
		StreamType:   0,
		TransferMode: 1,
		VoiceMode:    2,
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	createReply, err := (VoiceStreamCreateReply{MediaFlag: capturedMedia, CmdReply: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	start, err := (VoiceStreamStartRequest{MediaSession: capturedMedia}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	startReply, err := (VoiceStreamStartReply{Result: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stop, err := (VoiceStreamStopRequest{MediaSession: capturedMedia}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stopReply, err := (VoiceStreamStopReply{Result: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	destroy, err := (VoiceStreamDestroyRequest{MediaFlag: capturedMedia}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	destroyReply, err := (VoiceStreamDestroyReply{CmdReply: 1}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		cmd     Command
		id      int
		element string
		length  int
		attrs   string
	}{
		{"create", create, 148, "VoiceStreamCreateRequest", 180, `Channel="0" VoiceMode="2" TransferMode="1" StreamType="0"`},
		{"create reply", createReply, 149, "VoiceStreamCreateReply", 156, `MediaFlag="2003281556" CmdReply="0"`},
		{"start", start, 150, "VoiceStreamStartRequest", 147, `MediaSession="2003281556"`},
		{"start reply", startReply, 151, "VoiceStreamStartReply", 130, `Result="0"`},
		{"stop", stop, 152, "VoiceStreamStopRequest", 146, `MediaSession="2003281556"`},
		{"stop reply", stopReply, 153, "VoiceStreamStopReply", 129, `Result="0"`},
		{"destroy", destroy, 154, "VoiceStreamDestroyRequest", 146, `MediaFlag="2003281556"`},
		{"destroy reply", destroyReply, 155, "VoiceStreamDestroyReply", 134, `CmdReply="1"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cmd.ID != tc.id {
				t.Fatalf("ID = %d, quiero %d", tc.cmd.ID, tc.id)
			}
			if len(tc.cmd.Payload) != tc.length {
				t.Fatalf("longitud = %d, quiero %d\n%s", len(tc.cmd.Payload), tc.length, tc.cmd.Payload)
			}
			want := voiceDocument(tc.id, tc.element, tc.attrs)
			if string(tc.cmd.Payload) != want {
				t.Fatalf("XML =\n%q\nquiero\n%q", tc.cmd.Payload, want)
			}
			if tc.cmd.Payload[0] != '<' || bytesHasTCPMagic(tc.cmd.Payload) {
				t.Fatalf("el payload no es XML pelado")
			}
			if err := parseRoundTrip(tc.element, bytes.TrimSuffix(tc.cmd.Payload, []byte{0})); err != nil {
				t.Fatalf("sin el NUL final: %v", err)
			}
		})
	}

	if VoiceStreamCreateRequestID != 148 || VoiceStreamCreateReplyID != 149 ||
		VoiceStreamStartRequestID != 150 || VoiceStreamStartReplyID != 151 ||
		VoiceStreamStopRequestID != 152 || VoiceStreamStopReplyID != 153 ||
		VoiceStreamDestroyRequestID != 154 || VoiceStreamDestroyReplyID != 155 {
		t.Fatal("constantes de ID")
	}

	gotCreate, err := ParseVoiceStreamCreateRequest(create.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if gotCreate.Channel != 0 || gotCreate.VoiceMode != 2 || gotCreate.TransferMode != 1 || gotCreate.StreamType != 0 {
		t.Fatalf("%+v", gotCreate)
	}
	gotCreateReply, err := ParseVoiceStreamCreateReply(createReply.Payload)
	if err != nil || !gotCreateReply.Success() || gotCreateReply.MediaFlag != capturedMedia || gotCreateReply.CmdReply != 0 {
		t.Fatalf("%+v %v", gotCreateReply, err)
	}
	gotStart, err := ParseVoiceStreamStartRequest(start.Payload)
	if err != nil || gotStart.MediaSession != capturedMedia {
		t.Fatalf("%+v %v", gotStart, err)
	}
	gotStartReply, err := ParseVoiceStreamStartReply(startReply.Payload)
	if err != nil || !gotStartReply.Success() || gotStartReply.Result != 0 {
		t.Fatalf("%+v %v", gotStartReply, err)
	}
	gotStop, err := ParseVoiceStreamStopRequest(stop.Payload)
	if err != nil || gotStop.MediaSession != capturedMedia {
		t.Fatalf("%+v %v", gotStop, err)
	}
	if strings.Contains(string(stop.Payload), "MediaFlag") {
		t.Fatal("Stop usa MediaSession")
	}
	gotStopReply, err := ParseVoiceStreamStopReply(stopReply.Payload)
	if err != nil || !gotStopReply.Success() {
		t.Fatalf("%+v %v", gotStopReply, err)
	}
	gotDestroy, err := ParseVoiceStreamDestroyRequest(destroy.Payload)
	if err != nil || gotDestroy.MediaFlag != capturedMedia {
		t.Fatalf("%+v %v", gotDestroy, err)
	}
	gotDestroyReply, err := ParseVoiceStreamDestroyReply(destroyReply.Payload)
	if err != nil || gotDestroyReply.CmdReply != 1 {
		t.Fatalf("%+v %v", gotDestroyReply, err)
	}

	// Otra sesión capturada, CmdReply 0, sin relleno de ceros.
	other, err := (VoiceStreamDestroyReply{CmdReply: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Payload) != 134 {
		t.Fatalf("CmdReply 0 mide %d", len(other.Payload))
	}
	parsedOther, err := ParseVoiceStreamDestroyReply(other.Payload)
	if err != nil || parsedOther.CmdReply != 0 {
		t.Fatalf("%+v %v", parsedOther, err)
	}
	short, err := (VoiceStreamStartRequest{MediaSession: 30688200}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Payload) != 145 {
		t.Fatalf("MediaSession de 8 dígitos mide %d", len(short.Payload))
	}
}

func voiceDocument(id int, element, attrs string) string {
	return `<?xml version="1.0" encoding="GB2312" standalone="yes" ?>` + "\n" +
		`<Command ID="` + strconv.Itoa(id) + `">` + "\n" +
		`    <` + element + ` ` + attrs + ` />` + "\n" +
		`</Command>` + "\n\x00"
}

func parseRoundTrip(element string, payload []byte) error {
	var err error
	switch element {
	case "VoiceStreamCreateRequest":
		_, err = ParseVoiceStreamCreateRequest(payload)
	case "VoiceStreamCreateReply":
		_, err = ParseVoiceStreamCreateReply(payload)
	case "VoiceStreamStartRequest":
		_, err = ParseVoiceStreamStartRequest(payload)
	case "VoiceStreamStartReply":
		_, err = ParseVoiceStreamStartReply(payload)
	case "VoiceStreamStopRequest":
		_, err = ParseVoiceStreamStopRequest(payload)
	case "VoiceStreamStopReply":
		_, err = ParseVoiceStreamStopReply(payload)
	case "VoiceStreamDestroyRequest":
		_, err = ParseVoiceStreamDestroyRequest(payload)
	case "VoiceStreamDestroyReply":
		_, err = ParseVoiceStreamDestroyReply(payload)
	default:
		err = errors.New(element)
	}
	return err
}

func bytesHasTCPMagic(b []byte) bool {
	return bytes.HasPrefix(b, []byte{0x05, 0x01, 0x11, 0x54}) || bytes.HasPrefix(b, []byte{0x05, 0x01, 0x11, 0x50})
}

func TestChannelBaseZero(t *testing.T) {
	if _, err := (VoiceStreamCreateRequest{Channel: -1, VoiceMode: 2, TransferMode: 1, StreamType: 0}).Marshal(); !errors.Is(err, ErrNegativeChannel) {
		t.Fatalf("marshal -1: %v", err)
	}
	ok, err := (VoiceStreamCreateRequest{Channel: 0, VoiceMode: 2, TransferMode: 1, StreamType: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseVoiceStreamCreateRequest(ok.Payload); err != nil {
		t.Fatal(err)
	}
	ch3, err := (VoiceStreamCreateRequest{Channel: 3, VoiceMode: 2, TransferMode: 1, StreamType: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseVoiceStreamCreateRequest(ch3.Payload)
	if err != nil || parsed.Channel != 3 {
		t.Fatalf("%+v %v", parsed, err)
	}

	negative := strings.Replace(string(ok.Payload), `Channel="0"`, `Channel="-1"`, 1)
	if _, err := ParseVoiceStreamCreateRequest([]byte(negative)); !errors.Is(err, ErrNegativeChannel) {
		t.Fatalf("parse -1: %v", err)
	}
	missing := strings.Replace(string(ok.Payload), ` Channel="0"`, ``, 1)
	if _, err := ParseVoiceStreamCreateRequest([]byte(missing)); err == nil {
		t.Fatal("falta Channel y se aceptó")
	}
}

func TestReplyResults(t *testing.T) {
	start, err := (VoiceStreamStartReply{Result: 0}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(string(start.Payload), ` Result="0"`, ``, 1)
	if _, err := ParseVoiceStreamStartReply([]byte(missing)); !errors.Is(err, ErrResult) {
		t.Fatalf("sin Result: %v", err)
	}
	bad := strings.Replace(string(start.Payload), `Result="0"`, `Result="ok"`, 1)
	if _, err := ParseVoiceStreamStartReply([]byte(bad)); !errors.Is(err, ErrResult) {
		t.Fatalf("Result no entero: %v", err)
	}
	neg, err := (VoiceStreamStartReply{Result: -1}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseVoiceStreamStartReply(neg.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Success() || parsed.Result != -1 {
		t.Fatalf("%+v", parsed)
	}

	createFail, err := (VoiceStreamCreateReply{MediaFlag: capturedMedia, CmdReply: 2}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseVoiceStreamCreateReply(createFail.Payload)
	if err != nil || got.Success() || got.CmdReply != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	noReply := strings.Replace(string(createFail.Payload), ` CmdReply="2"`, ``, 1)
	if _, err := ParseVoiceStreamCreateReply([]byte(noReply)); !errors.Is(err, ErrResult) {
		t.Fatalf("sin CmdReply: %v", err)
	}

	stopFail, err := (VoiceStreamStopReply{Result: 5}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	stop, err := ParseVoiceStreamStopReply(stopFail.Payload)
	if err != nil || stop.Success() {
		t.Fatalf("%+v %v", stop, err)
	}

	extra := strings.Replace(string(start.Payload), `Result="0"`, `Result="0" Extra="1"`, 1)
	if _, err := ParseVoiceStreamStartReply([]byte(extra)); err == nil || !strings.Contains(err.Error(), "no reconocidos") {
		t.Fatalf("atributo extra: %v", err)
	}
	wrongID := strings.Replace(string(start.Payload), `ID="151"`, `ID="150"`, 1)
	if _, err := ParseVoiceStreamStartReply([]byte(wrongID)); err == nil {
		t.Fatal("ID cruzado aceptado")
	}
}
