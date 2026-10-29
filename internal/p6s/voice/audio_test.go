package voice

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestAudioExtensionRequires44(t *testing.T) {
	if AudioExtensionLen != 44 {
		t.Fatalf("AudioExtensionLen = %d", AudioExtensionLen)
	}
	for _, n := range []int{0, 1, 43, 45, 32, 36, 76} {
		if _, err := ParseAudioExtension(make([]byte, n)); !errors.Is(err, ErrAudioExtension) {
			t.Errorf("len %d: %v", n, err)
		}
	}
	if _, err := ParseAudioExtension(nil); !errors.Is(err, ErrAudioExtension) {
		t.Errorf("nil: %v", err)
	}

	raw := make([]byte, 44)
	for i := range raw {
		raw[i] = byte(i*17 + 3)
	}
	// Patrón que no se interpreta: bytes que parecerían un entero BE y 0x140.
	raw[0], raw[1], raw[2], raw[3] = 0x77, 0x67, 0xA6, 0x94
	raw[8], raw[9] = 0x01, 0x40

	ext, err := ParseAudioExtension(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ext != AudioExtension(array44(raw)) {
		t.Fatal("la extensión no conserva los 44 bytes")
	}
	if !bytes.Equal(ext.Bytes(), raw) {
		t.Fatal("Bytes() alteró el contenido")
	}
	ext.Bytes()[0] = 0
	if ext[0] != 0x77 {
		t.Fatal("Bytes() devolvió la memoria interna")
	}
}

func array44(b []byte) [44]byte {
	var out [44]byte
	copy(out[:], b)
	return out
}

func TestPCMUPayloadIsBare320(t *testing.T) {
	if FrameDuration != 40*time.Millisecond {
		t.Fatal(FrameDuration)
	}
	ok := make([]byte, 320)
	if err := ValidatePCMUPayload(ok); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 319, 321, 640, 32 + 44 + 320, 36} {
		if err := ValidatePCMUPayload(make([]byte, n)); !errors.Is(err, ErrPCMULength) {
			t.Errorf("len %d: %v", n, err)
		}
	}
	// Una trama TCP de medios (cabecera de 32 + extensión de 44 + 320) no
	// se desempaqueta aquí: la carga sola mide 320 y el conjunto se rechaza.
	framed := make([]byte, 32+44+320)
	copy(framed, []byte{0x05, 0x01, 0x11, 0x50})
	if err := ValidatePCMUPayload(framed); !errors.Is(err, ErrPCMULength) {
		t.Fatalf("trama con cabecera: %v", err)
	}
	payload := framed[32+44:]
	if err := ValidatePCMUPayload(payload); err != nil {
		t.Fatal(err)
	}
}
