package mpegps_test

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/belcaik/open-nvr/internal/p6s/mpegps"
)

func TestVideoExtensionGolden(t *testing.T) {
	golden := readGolden(t)
	raw := golden[32:76]
	extension, err := mpegps.ParseVideoExtension(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extension.Bytes(), raw) {
		t.Fatal("la extensión no conserva los bytes de captura")
	}

	raw[0] ^= 0xff
	if extension.Bytes()[0] != 0 {
		t.Fatal("la extensión comparte memoria con la entrada")
	}
	got := extension.Bytes()
	got[1] ^= 0xff
	if extension.Bytes()[1] != 0 {
		t.Fatal("Bytes expone memoria mutable de la extensión")
	}
}

func TestParserDoesNotInventUnitsFromRawVideoGolden(t *testing.T) {
	golden := readGolden(t)
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(golden); {
		extensionLength := int(golden[offset+8])<<24 | int(golden[offset+9])<<16 | int(golden[offset+10])<<8 | int(golden[offset+11])
		payloadLength := int(golden[offset+12])<<24 | int(golden[offset+13])<<16 | int(golden[offset+14])<<8 | int(golden[offset+15])
		extension, err := mpegps.ParseVideoExtension(golden[offset+32 : offset+32+extensionLength])
		if err != nil {
			t.Fatal(err)
		}
		payload := golden[offset+32+extensionLength : offset+32+extensionLength+payloadLength]
		units, err := parser.Push(mpegps.Frame{Extension: extension, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		if len(units) != 0 {
			t.Fatalf("vídeo crudo produjo %d unidades MPEG-PS", len(units))
		}
		offset += 32 + extensionLength + payloadLength
	}
}

func TestPackSurvivesByteFragmentationAndDecodesSCR(t *testing.T) {
	packet := []byte{0x00, 0x00, 0x01, 0xba, 0x44, 0x00, 0x16, 0xfc, 0x86, 0x59, 0x00, 0xc0, 0x00, 0x00}
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var units []mpegps.Unit
	for _, b := range packet {
		got, err := parser.Push(mpegps.Frame{Payload: []byte{b}})
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, got...)
	}
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1", len(units))
	}
	unit := units[0]
	if unit.Kind != mpegps.UnitPack {
		t.Fatalf("kind = %v, want pack", unit.Kind)
	}
	if !bytes.Equal(unit.Bytes, packet) {
		t.Fatalf("bytes = %x, want %x", unit.Bytes, packet)
	}
	if unit.SCR == nil || unit.SCR.Base != 90000 || unit.SCR.Extension != 300 {
		t.Fatalf("SCR = %+v, want base 90000 extension 300", unit.SCR)
	}
}

func TestPESDecodesPTSAndDTSWhenPresent(t *testing.T) {
	packet := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x0d, 0x80, 0xc0, 0x0a, 0x21, 0x00, 0x05, 0xbf, 0x21, 0x01, 0x00, 0x03, 0x5f, 0x91}
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	units, err := parser.Push(mpegps.Frame{Payload: packet})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0].Kind != mpegps.UnitPES {
		t.Fatalf("units = %+v, want one PES", units)
	}
	unit := units[0]
	if unit.StreamID != 0xe0 {
		t.Fatalf("stream ID = %02x, want e0", unit.StreamID)
	}
	if unit.PTS == nil || *unit.PTS != 90000 {
		t.Fatalf("PTS = %v, want 90000", unit.PTS)
	}
	if unit.DTS == nil || *unit.DTS != 45000 {
		t.Fatalf("DTS = %v, want 45000", unit.DTS)
	}
	if !bytes.Equal(unit.Bytes, packet) {
		t.Fatalf("bytes = %x, want %x", unit.Bytes, packet)
	}
}

func TestPESWithoutTimestampsFollowedByProgramEnd(t *testing.T) {
	pes := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x03, 0x80, 0x80, 0x00}
	end := []byte{0x00, 0x00, 0x01, 0xb9}
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	units, err := parser.Push(mpegps.Frame{Payload: pes})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parser.Push(mpegps.Frame{Payload: end})
	if err != nil {
		t.Fatal(err)
	}
	units = append(units, got...)
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2", len(units))
	}
	if units[0].PTS != nil || units[0].DTS != nil {
		t.Fatalf("timestamps inventados: PTS %v DTS %v", units[0].PTS, units[0].DTS)
	}
	if units[1].Kind != mpegps.UnitProgramEnd || !bytes.Equal(units[1].Bytes, end) {
		t.Fatalf("last unit = %+v, want program_end_code", units[1])
	}
}

func TestZeroLengthPESReassemblesCapturedVideoAcrossFrames(t *testing.T) {
	golden := readGolden(t)
	header := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x00, 0x80, 0x80, 0x05, 0x21, 0x00, 0x05, 0xbf, 0x21}
	end := []byte{0x00, 0x00, 0x01, 0xb9}
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	chunks := [][]byte{
		header[:5],
		append(append([]byte(nil), header[5:]...), golden[76:424]...),
		golden[500:848],
		end,
	}
	var units []mpegps.Unit
	for _, chunk := range chunks {
		got, err := parser.Push(mpegps.Frame{Payload: chunk})
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, got...)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want PES and program end", len(units))
	}
	want := append(append(append([]byte(nil), header...), golden[76:424]...), golden[500:848]...)
	if units[0].Kind != mpegps.UnitPES || !bytes.Equal(units[0].Bytes, want) {
		t.Fatal("el PES no conserva cabecera y bytes de vídeo de captura")
	}
	if units[0].PTS == nil || *units[0].PTS != 90000 || units[0].DTS != nil {
		t.Fatalf("PTS %v DTS %v", units[0].PTS, units[0].DTS)
	}
	if units[1].Kind != mpegps.UnitProgramEnd {
		t.Fatalf("kind = %v, want program end", units[1].Kind)
	}
}

func TestPESDoesNotReadTimestampsFromFollowingPacket(t *testing.T) {
	short := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x03, 0x80, 0xc0, 0x00}
	following := []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x03, 0x80, 0x80, 0x00}
	parser, err := mpegps.NewParser(mpegps.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	units, err := parser.Push(mpegps.Frame{Payload: append(short, following...)})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2", len(units))
	}
	if units[0].PTS != nil || units[0].DTS != nil {
		t.Fatalf("timestamps tomados del siguiente PES: PTS %v DTS %v", units[0].PTS, units[0].DTS)
	}
}

func TestParserEnforcesBufferAndPacketLimits(t *testing.T) {
	parser, err := mpegps.NewParser(mpegps.Limits{MaxBufferedBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Push(mpegps.Frame{Payload: make([]byte, 9)})
	if !errors.Is(err, mpegps.ErrBufferLimit) {
		t.Fatalf("buffer error = %v, want ErrBufferLimit", err)
	}

	parser, err = mpegps.NewParser(mpegps.Limits{MaxPacketBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	_, err = parser.Push(mpegps.Frame{Payload: []byte{0x00, 0x00, 0x01, 0xe0, 0x00, 0x03, 0x80, 0x80, 0x00}})
	if !errors.Is(err, mpegps.ErrPacketLimit) {
		t.Fatalf("packet error = %v, want ErrPacketLimit", err)
	}
}

func readGolden(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/p6s-video-frames.bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 848 {
		t.Fatalf("golden: got %d bytes, want 848", len(data))
	}
	return data
}
