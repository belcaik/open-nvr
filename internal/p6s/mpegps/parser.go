package mpegps

import "bytes"

const (
	DefaultMaxBufferedBytes = 16 << 20
	DefaultMaxPacketBytes   = 16 << 20
)

// Limits acota la memoria y la resincronización del parser. Cero usa el default.
type Limits struct {
	MaxBufferedBytes int
	MaxPacketBytes   int
}

// Frame contiene una extensión P6S opaca y su payload MPEG-PS fragmentado.
type Frame struct {
	Extension VideoExtension
	Payload   []byte
}

type UnitKind uint8

const (
	UnitPack UnitKind = iota + 1
	UnitPES
	UnitProgramEnd
)

// SCR es el reloj de 90 kHz y su extensión de 27 MHz presentes en un pack.
type SCR struct {
	Base      uint64
	Extension uint16
}

// Unit es una estructura MPEG-PS completa reconstruida por Parser.
type Unit struct {
	Kind     UnitKind
	Bytes    []byte
	SCR      *SCR
	StreamID byte
	PTS      *uint64
	DTS      *uint64
}

type Parser struct {
	buffer    []byte
	maxBuffer int
	maxPacket int
}

func NewParser(limits Limits) (*Parser, error) {
	if limits.MaxBufferedBytes < 0 || limits.MaxPacketBytes < 0 {
		return nil, ErrInvalidLimit
	}
	if limits.MaxBufferedBytes == 0 {
		limits.MaxBufferedBytes = DefaultMaxBufferedBytes
	}
	if limits.MaxPacketBytes == 0 {
		limits.MaxPacketBytes = DefaultMaxPacketBytes
	}
	return &Parser{maxBuffer: limits.MaxBufferedBytes, maxPacket: limits.MaxPacketBytes}, nil
}

// Push incorpora una trama y devuelve las unidades que hayan quedado completas.
func (p *Parser) Push(frame Frame) ([]Unit, error) {
	if len(frame.Payload) > p.maxBuffer-len(p.buffer) {
		return nil, &LimitError{Field: "búfer", Length: len(p.buffer) + len(frame.Payload), Limit: p.maxBuffer, Err: ErrBufferLimit}
	}
	p.buffer = append(p.buffer, frame.Payload...)
	var units []Unit
	for {
		if len(p.buffer) < 4 {
			return units, nil
		}
		index := bytes.Index(p.buffer, []byte{0x00, 0x00, 0x01})
		if index < 0 {
			keep := 0
			for keep < 2 && keep < len(p.buffer) && p.buffer[len(p.buffer)-1-keep] == 0 {
				keep++
			}
			copy(p.buffer, p.buffer[len(p.buffer)-keep:])
			p.buffer = p.buffer[:keep]
			return units, nil
		}
		if index > 0 {
			p.buffer = p.buffer[index:]
		}
		if len(p.buffer) < 4 {
			return units, nil
		}
		switch p.buffer[3] {
		case 0xba:
			if len(p.buffer) < 14 {
				return units, nil
			}
			data := append([]byte(nil), p.buffer[:14]...)
			scr := decodeSCR(p.buffer[4:10])
			units = append(units, Unit{Kind: UnitPack, Bytes: data, SCR: &scr})
			p.buffer = p.buffer[14:]
		case 0xb9:
			units = append(units, Unit{Kind: UnitProgramEnd, Bytes: append([]byte(nil), p.buffer[:4]...)})
			p.buffer = p.buffer[4:]
		default:
			if p.buffer[3] < 0xe0 || p.buffer[3] > 0xef {
				p.buffer = p.buffer[1:]
				continue
			}
			if len(p.buffer) < 9 {
				return units, nil
			}
			length := int(p.buffer[4])<<8 | int(p.buffer[5])
			size := 6 + length
			if length == 0 {
				size = -1
				for _, code := range []byte{0xba, 0xb9} {
					if index := bytes.Index(p.buffer[6:], []byte{0x00, 0x00, 0x01, code}); index >= 0 && (size < 0 || index+6 < size) {
						size = index + 6
					}
				}
				if size < 0 {
					if len(p.buffer) > p.maxPacket {
						return units, &LimitError{Field: "paquete", Length: len(p.buffer), Limit: p.maxPacket, Err: ErrPacketLimit}
					}
					return units, nil
				}
			}
			if size > p.maxPacket {
				return units, &LimitError{Field: "paquete", Length: size, Limit: p.maxPacket, Err: ErrPacketLimit}
			}
			if len(p.buffer) < size {
				return units, nil
			}
			unit := Unit{Kind: UnitPES, Bytes: append([]byte(nil), p.buffer[:size]...), StreamID: p.buffer[3]}
			flags := p.buffer[7] & 0xc0
			headerLength := 9 + int(p.buffer[8])
			if flags != 0 && headerLength >= 14 && size >= 14 {
				pts := decodeTimestamp(p.buffer[9:14])
				unit.PTS = &pts
			}
			if flags == 0xc0 && headerLength >= 19 && size >= 19 {
				dts := decodeTimestamp(p.buffer[14:19])
				unit.DTS = &dts
			}
			units = append(units, unit)
			p.buffer = p.buffer[size:]
		}
	}
}

func decodeTimestamp(b []byte) uint64 {
	value := uint64(b[0]>>1&0x07) << 30
	value |= uint64(b[1]) << 22
	value |= uint64(b[2]>>1) << 15
	value |= uint64(b[3]) << 7
	value |= uint64(b[4] >> 1)
	return value
}

func decodeSCR(b []byte) SCR {
	base := uint64(b[0]&0x38) << 27
	base |= uint64(b[0]&0x03) << 28
	base |= uint64(b[1]) << 20
	base |= uint64(b[2]&0xf8) << 12
	base |= uint64(b[2]&0x03) << 13
	base |= uint64(b[3]) << 5
	base |= uint64(b[4] >> 3)
	extension := uint16(b[4]&0x03) << 7
	extension |= uint16(b[5] >> 1)
	return SCR{Base: base, Extension: extension}
}
