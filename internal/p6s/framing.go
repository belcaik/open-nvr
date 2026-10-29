package p6s

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	CommandHeaderSize = 36
	MediaHeaderSize   = 32

	ProtocolVersion uint32 = 0x00001001
	MessageType     uint32 = 3

	DefaultMaxCommandXML     uint32 = 1 << 20
	DefaultMaxMediaExtension uint32 = 64 << 10
	DefaultMaxMediaPayload   uint32 = 16 << 20
)

const (
	commandMagic uint32 = 0x05011154
	mediaMagic   uint32 = 0x05011150
)

var (
	ErrInvalidMagic    = errors.New("p6s: invalid magic")
	ErrInvalidVersion  = errors.New("p6s: invalid version")
	ErrInvalidType     = errors.New("p6s: invalid message type")
	ErrInvalidReserved = errors.New("p6s: nonzero reserved header field")
	ErrLengthLimit     = errors.New("p6s: length exceeds limit")
)

// HeaderError describes a malformed fixed header. It deliberately omits the
// message body so errors cannot disclose XML or media content.
type HeaderError struct {
	Field string
	Err   error
}

func (e *HeaderError) Error() string { return "p6s: invalid " + e.Field }
func (e *HeaderError) Unwrap() error { return e.Err }

// LengthError is returned before allocating or reading an oversized field.
type LengthError struct {
	Field  string
	Length uint32
	Limit  uint32
}

func (e *LengthError) Error() string {
	return fmt.Sprintf("p6s: %s length %d exceeds limit %d", e.Field, e.Length, e.Limit)
}
func (e *LengthError) Unwrap() error { return ErrLengthLimit }

// CommandLimits bounds memory allocated for an incoming or outgoing command.
// A zero MaxXML selects DefaultMaxCommandXML.
type CommandLimits struct {
	MaxXML uint32
}

// MediaLimits bounds memory allocated for an incoming or outgoing media
// frame. A zero field selects its corresponding default.
type MediaLimits struct {
	MaxExtension uint32
	MaxPayload   uint32
}

// Command is one complete command-channel message.
type Command struct {
	Sequence uint32
	XML      []byte
}

// MediaFrame is one complete media-channel message.
type MediaFrame struct {
	Extension []byte
	Payload   []byte
}

// MediaLinkFrame constructs the first media frame that associates a media
// connection with its command-channel MediaFlag.
func MediaLinkFrame(mediaFlag uint32) MediaFrame {
	extension := make([]byte, 4)
	binary.BigEndian.PutUint32(extension, mediaFlag)
	return MediaFrame{Extension: extension}
}

// ReadCommand reads exactly one command. Supplying no limits uses safe
// defaults; at most one limits value may be supplied.
func ReadCommand(r io.Reader, limits ...CommandLimits) (Command, error) {
	limit, err := commandLimit(limits)
	if err != nil {
		return Command{}, err
	}
	header := make([]byte, CommandHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Command{}, fmt.Errorf("p6s: read command header: %w", err)
	}
	if err := validateHeader(header, commandMagic); err != nil {
		return Command{}, err
	}
	length := binary.BigEndian.Uint32(header[16:20])
	if length > limit {
		return Command{}, &LengthError{Field: "command XML", Length: length, Limit: limit}
	}
	if uint64(length) > uint64(maxInt()) {
		return Command{}, &LengthError{Field: "command XML", Length: length, Limit: uint32(maxInt())}
	}
	xml := make([]byte, int(length))
	if _, err := io.ReadFull(r, xml); err != nil {
		return Command{}, fmt.Errorf("p6s: read command XML: %w", err)
	}
	return Command{Sequence: binary.BigEndian.Uint32(header[8:12]), XML: xml}, nil
}

// WriteCommand writes one command, retrying short writes.
func WriteCommand(w io.Writer, command Command, limits ...CommandLimits) error {
	limit, err := commandLimit(limits)
	if err != nil {
		return err
	}
	length, err := checkedLength("command XML", len(command.XML), limit)
	if err != nil {
		return err
	}
	header := newHeader(CommandHeaderSize, commandMagic)
	binary.BigEndian.PutUint32(header[8:12], command.Sequence)
	binary.BigEndian.PutUint32(header[16:20], length)
	if err := writeFull(w, header); err != nil {
		return fmt.Errorf("p6s: write command header: %w", err)
	}
	if err := writeFull(w, command.XML); err != nil {
		return fmt.Errorf("p6s: write command XML: %w", err)
	}
	return nil
}

// ReadMediaFrame reads exactly one media frame. Supplying no limits uses safe
// defaults; at most one limits value may be supplied.
func ReadMediaFrame(r io.Reader, limits ...MediaLimits) (MediaFrame, error) {
	extLimit, payloadLimit, err := mediaLimits(limits)
	if err != nil {
		return MediaFrame{}, err
	}
	header := make([]byte, MediaHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return MediaFrame{}, fmt.Errorf("p6s: read media header: %w", err)
	}
	if err := validateHeader(header, mediaMagic); err != nil {
		return MediaFrame{}, err
	}
	extLength := binary.BigEndian.Uint32(header[8:12])
	payloadLength := binary.BigEndian.Uint32(header[12:16])
	if extLength > extLimit {
		return MediaFrame{}, &LengthError{Field: "media extension", Length: extLength, Limit: extLimit}
	}
	if payloadLength > payloadLimit {
		return MediaFrame{}, &LengthError{Field: "media payload", Length: payloadLength, Limit: payloadLimit}
	}
	if uint64(extLength) > uint64(maxInt()) {
		return MediaFrame{}, &LengthError{Field: "media extension", Length: extLength, Limit: uint32(maxInt())}
	}
	if uint64(payloadLength) > uint64(maxInt()) {
		return MediaFrame{}, &LengthError{Field: "media payload", Length: payloadLength, Limit: uint32(maxInt())}
	}
	extension := make([]byte, int(extLength))
	if _, err := io.ReadFull(r, extension); err != nil {
		return MediaFrame{}, fmt.Errorf("p6s: read media extension: %w", err)
	}
	payload := make([]byte, int(payloadLength))
	if _, err := io.ReadFull(r, payload); err != nil {
		return MediaFrame{}, fmt.Errorf("p6s: read media payload: %w", err)
	}
	return MediaFrame{Extension: extension, Payload: payload}, nil
}

// WriteMediaFrame writes one media frame, retrying short writes.
func WriteMediaFrame(w io.Writer, frame MediaFrame, limits ...MediaLimits) error {
	extLimit, payloadLimit, err := mediaLimits(limits)
	if err != nil {
		return err
	}
	extLength, err := checkedLength("media extension", len(frame.Extension), extLimit)
	if err != nil {
		return err
	}
	payloadLength, err := checkedLength("media payload", len(frame.Payload), payloadLimit)
	if err != nil {
		return err
	}
	header := newHeader(MediaHeaderSize, mediaMagic)
	binary.BigEndian.PutUint32(header[8:12], extLength)
	binary.BigEndian.PutUint32(header[12:16], payloadLength)
	if err := writeFull(w, header); err != nil {
		return fmt.Errorf("p6s: write media header: %w", err)
	}
	if err := writeFull(w, frame.Extension); err != nil {
		return fmt.Errorf("p6s: write media extension: %w", err)
	}
	if err := writeFull(w, frame.Payload); err != nil {
		return fmt.Errorf("p6s: write media payload: %w", err)
	}
	return nil
}

func newHeader(size int, magic uint32) []byte {
	header := make([]byte, size)
	binary.BigEndian.PutUint32(header[0:4], magic)
	binary.BigEndian.PutUint32(header[4:8], ProtocolVersion)
	typeOffset := 16
	if size == CommandHeaderSize {
		typeOffset = 20
	}
	binary.BigEndian.PutUint32(header[typeOffset:typeOffset+4], MessageType)
	return header
}

func validateHeader(header []byte, magic uint32) error {
	if binary.BigEndian.Uint32(header[0:4]) != magic {
		return &HeaderError{Field: "magic", Err: ErrInvalidMagic}
	}
	if binary.BigEndian.Uint32(header[4:8]) != ProtocolVersion {
		return &HeaderError{Field: "version", Err: ErrInvalidVersion}
	}
	typeOffset := 16
	if len(header) == CommandHeaderSize {
		typeOffset = 20
	}
	if binary.BigEndian.Uint32(header[typeOffset:typeOffset+4]) != MessageType {
		return &HeaderError{Field: "type", Err: ErrInvalidType}
	}
	reservedStart := 20
	if len(header) == CommandHeaderSize {
		if binary.BigEndian.Uint32(header[12:16]) != 0 {
			return &HeaderError{Field: "reserved", Err: ErrInvalidReserved}
		}
		reservedStart = 24
	}
	for _, value := range header[reservedStart:] {
		if value != 0 {
			return &HeaderError{Field: "reserved", Err: ErrInvalidReserved}
		}
	}
	return nil
}

func commandLimit(limits []CommandLimits) (uint32, error) {
	if len(limits) > 1 {
		return 0, errors.New("p6s: multiple command limits")
	}
	if len(limits) == 0 || limits[0].MaxXML == 0 {
		return DefaultMaxCommandXML, nil
	}
	return limits[0].MaxXML, nil
}

func mediaLimits(limits []MediaLimits) (uint32, uint32, error) {
	if len(limits) > 1 {
		return 0, 0, errors.New("p6s: multiple media limits")
	}
	if len(limits) == 0 {
		return DefaultMaxMediaExtension, DefaultMaxMediaPayload, nil
	}
	extension := limits[0].MaxExtension
	payload := limits[0].MaxPayload
	if extension == 0 {
		extension = DefaultMaxMediaExtension
	}
	if payload == 0 {
		payload = DefaultMaxMediaPayload
	}
	return extension, payload, nil
}

func checkedLength(field string, length int, limit uint32) (uint32, error) {
	if uint64(length) > uint64(^uint32(0)) || uint64(length) > uint64(limit) {
		reported := uint32(length)
		if uint64(length) > uint64(^uint32(0)) {
			reported = ^uint32(0)
		}
		return 0, &LengthError{Field: field, Length: reported, Limit: limit}
	}
	return uint32(length), nil
}

func maxInt() int { return int(^uint(0) >> 1) }

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
