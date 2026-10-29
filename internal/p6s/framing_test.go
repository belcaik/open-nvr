package p6s

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCommandGoldenAndByteFragmentation(t *testing.T) {
	command := Command{Sequence: 0x12345678, XML: []byte("<Command/>")}
	want := decodeHex(t, "050111540000100112345678000000000000000a000000030000000000000000000000003c436f6d6d616e642f3e")

	var encoded bytes.Buffer
	if err := WriteCommand(&encoded, command); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), want) {
		t.Fatalf("encoded command = %x, want %x", encoded.Bytes(), want)
	}

	got, err := ReadCommand(oneByteReader{bytes.NewReader(want)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != command.Sequence || !bytes.Equal(got.XML, command.XML) {
		t.Fatalf("decoded command = %#v, want %#v", got, command)
	}
}

func TestMediaGoldenLinkAndByteFragmentation(t *testing.T) {
	frame := MediaLinkFrame(0x7767a694)
	want := decodeHex(t, "05011150000010010000000400000000000000030000000000000000000000007767a694")

	var encoded bytes.Buffer
	if err := WriteMediaFrame(&encoded, frame); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), want) {
		t.Fatalf("encoded media link = %x, want %x", encoded.Bytes(), want)
	}

	got, err := ReadMediaFrame(oneByteReader{bytes.NewReader(want)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Extension, frame.Extension) || len(got.Payload) != 0 {
		t.Fatalf("decoded media frame = %#v, want %#v", got, frame)
	}
}

func TestMediaPayloadRoundTripWithShortWrites(t *testing.T) {
	want := MediaFrame{Extension: []byte{1, 2, 3}, Payload: []byte{4, 5, 6, 7}}
	var destination bytes.Buffer
	writer := oneByteWriter{&destination}
	if err := WriteMediaFrame(writer, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMediaFrame(oneByteReader{bytes.NewReader(destination.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Extension, want.Extension) || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestReadersRejectInvalidHeaders(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		read func([]byte) error
		want error
	}{
		{
			name: "command magic",
			data: commandHeader(0, ProtocolVersion, MessageType, 0),
			read: func(data []byte) error { _, err := ReadCommand(bytes.NewReader(data)); return err },
			want: ErrInvalidMagic,
		},
		{
			name: "command version",
			data: commandHeader(commandMagic, 2, MessageType, 0),
			read: func(data []byte) error { _, err := ReadCommand(bytes.NewReader(data)); return err },
			want: ErrInvalidVersion,
		},
		{
			name: "media type",
			data: mediaHeader(mediaMagic, ProtocolVersion, 9, 0, 0),
			read: func(data []byte) error { _, err := ReadMediaFrame(bytes.NewReader(data)); return err },
			want: ErrInvalidType,
		},
		{
			name: "media magic",
			data: mediaHeader(0, ProtocolVersion, MessageType, 0, 0),
			read: func(data []byte) error { _, err := ReadMediaFrame(bytes.NewReader(data)); return err },
			want: ErrInvalidMagic,
		},
		{
			name: "media version",
			data: mediaHeader(mediaMagic, 2, MessageType, 0, 0),
			read: func(data []byte) error { _, err := ReadMediaFrame(bytes.NewReader(data)); return err },
			want: ErrInvalidVersion,
		},
		{
			name: "media reserved",
			data: func() []byte { h := mediaHeader(mediaMagic, ProtocolVersion, MessageType, 0, 0); h[31] = 1; return h }(),
			read: func(data []byte) error { _, err := ReadMediaFrame(bytes.NewReader(data)); return err },
			want: ErrInvalidReserved,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.read(test.data); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestReadersEnforceConfiguredLengthsBeforeReadingBodies(t *testing.T) {
	command := commandHeader(commandMagic, ProtocolVersion, MessageType, 9)
	_, err := ReadCommand(bytes.NewReader(command), CommandLimits{MaxXML: 8})
	assertLengthError(t, err, "command XML", 9, 8)

	media := mediaHeader(mediaMagic, ProtocolVersion, MessageType, 5, 7)
	_, err = ReadMediaFrame(bytes.NewReader(media), MediaLimits{MaxExtension: 4, MaxPayload: 8})
	assertLengthError(t, err, "media extension", 5, 4)

	media = mediaHeader(mediaMagic, ProtocolVersion, MessageType, 4, 9)
	_, err = ReadMediaFrame(bytes.NewReader(media), MediaLimits{MaxExtension: 4, MaxPayload: 8})
	assertLengthError(t, err, "media payload", 9, 8)
}

func TestTruncatedFramesReturnUnexpectedEOF(t *testing.T) {
	command := append(commandHeader(commandMagic, ProtocolVersion, MessageType, 2), 'x')
	if _, err := ReadCommand(bytes.NewReader(command)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("command error = %v, want unexpected EOF", err)
	}

	media := append(mediaHeader(mediaMagic, ProtocolVersion, MessageType, 2, 0), 'x')
	if _, err := ReadMediaFrame(bytes.NewReader(media)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("media error = %v, want unexpected EOF", err)
	}
}

func TestErrorsDoNotContainContents(t *testing.T) {
	const secret = "super-secret-password"
	err := WriteCommand(failingWriter{}, Command{XML: []byte(secret)})
	if err == nil {
		t.Fatal("WriteCommand unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error disclosed command contents: %v", err)
	}
}

type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

type oneByteWriter struct{ io.Writer }

func (w oneByteWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return w.Writer.Write(p)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("test write failure") }

func commandHeader(magic, version, messageType, length uint32) []byte {
	header := make([]byte, CommandHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magic)
	binary.BigEndian.PutUint32(header[4:8], version)
	binary.BigEndian.PutUint32(header[16:20], length)
	binary.BigEndian.PutUint32(header[20:24], messageType)
	return header
}

func mediaHeader(magic, version, messageType, extension, payload uint32) []byte {
	header := make([]byte, MediaHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], magic)
	binary.BigEndian.PutUint32(header[4:8], version)
	binary.BigEndian.PutUint32(header[8:12], extension)
	binary.BigEndian.PutUint32(header[12:16], payload)
	binary.BigEndian.PutUint32(header[16:20], messageType)
	return header
}

func assertLengthError(t *testing.T, err error, field string, length, limit uint32) {
	t.Helper()
	var lengthErr *LengthError
	if !errors.As(err, &lengthErr) {
		t.Fatalf("error = %v, want LengthError", err)
	}
	if lengthErr.Field != field || lengthErr.Length != length || lengthErr.Limit != limit {
		t.Fatalf("LengthError = %#v, want field=%q length=%d limit=%d", lengthErr, field, length, limit)
	}
	if !errors.Is(err, ErrLengthLimit) {
		t.Fatalf("error = %v, want ErrLengthLimit", err)
	}
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	result := make([]byte, len(value)/2)
	for i := range result {
		var high, low byte
		for j, target := range []*byte{&high, &low} {
			c := value[2*i+j]
			switch {
			case c >= '0' && c <= '9':
				*target = c - '0'
			case c >= 'a' && c <= 'f':
				*target = c - 'a' + 10
			default:
				t.Fatalf("invalid hex %q", value)
			}
		}
		result[i] = high<<4 | low
	}
	return result
}
