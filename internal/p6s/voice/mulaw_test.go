package voice

import (
	"encoding/binary"
	"testing"
	"time"
)

// muLawLinear es la tabla de decodificación G.711 mu-law. Los extremos y el
// cero se comprobaron aparte: 0x00 = -32124, 0x7F = 0, 0x80 = 32124, 0xFF = 0.
var muLawLinear = [256]int16{
	-32124, -31100, -30076, -29052, -28028, -27004, -25980, -24956,
	-23932, -22908, -21884, -20860, -19836, -18812, -17788, -16764,
	-15996, -15484, -14972, -14460, -13948, -13436, -12924, -12412,
	-11900, -11388, -10876, -10364, -9852, -9340, -8828, -8316,
	-7932, -7676, -7420, -7164, -6908, -6652, -6396, -6140,
	-5884, -5628, -5372, -5116, -4860, -4604, -4348, -4092,
	-3900, -3772, -3644, -3516, -3388, -3260, -3132, -3004,
	-2876, -2748, -2620, -2492, -2364, -2236, -2108, -1980,
	-1884, -1820, -1756, -1692, -1628, -1564, -1500, -1436,
	-1372, -1308, -1244, -1180, -1116, -1052, -988, -924,
	-876, -844, -812, -780, -748, -716, -684, -652,
	-620, -588, -556, -524, -492, -460, -428, -396,
	-372, -356, -340, -324, -308, -292, -276, -260,
	-244, -228, -212, -196, -180, -164, -148, -132,
	-120, -112, -104, -96, -88, -80, -72, -64,
	-56, -48, -40, -32, -24, -16, -8, 0,
	32124, 31100, 30076, 29052, 28028, 27004, 25980, 24956,
	23932, 22908, 21884, 20860, 19836, 18812, 17788, 16764,
	15996, 15484, 14972, 14460, 13948, 13436, 12924, 12412,
	11900, 11388, 10876, 10364, 9852, 9340, 8828, 8316,
	7932, 7676, 7420, 7164, 6908, 6652, 6396, 6140,
	5884, 5628, 5372, 5116, 4860, 4604, 4348, 4092,
	3900, 3772, 3644, 3516, 3388, 3260, 3132, 3004,
	2876, 2748, 2620, 2492, 2364, 2236, 2108, 1980,
	1884, 1820, 1756, 1692, 1628, 1564, 1500, 1436,
	1372, 1308, 1244, 1180, 1116, 1052, 988, 924,
	876, 844, 812, 780, 748, 716, 684, 652,
	620, 588, 556, 524, 492, 460, 428, 396,
	372, 356, 340, 324, 308, 292, 276, 260,
	244, 228, 212, 196, 180, 164, 148, 132,
	120, 112, 104, 96, 88, 80, 72, 64,
	56, 48, 40, 32, 24, 16, 8, 0,
}

// muExpLUT es la tabla de segmentos de la codificación mu-law de Sun, usada
// solo como referencia independiente del bucle de LinearToPCMU.
var muExpLUT = [256]int{
	0, 0, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3,
	4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
}

func referenceLinearToPCMU(sample int16) byte {
	const (
		bias = 0x84
		clip = 32635
	)
	mag := int(sample)
	sign := 0
	if mag < 0 {
		sign = 0x80
		mag = -mag
	}
	if mag > clip {
		mag = clip
	}
	mag += bias
	exp := muExpLUT[(mag>>7)&0xFF]
	mantissa := (mag >> (exp + 3)) & 0x0F
	return ^(byte(sign) | byte(exp<<4) | byte(mantissa))
}

func TestMulawGoldenExtremesAndZero(t *testing.T) {
	zeros := []struct {
		sample int16
		code   byte
	}{
		{0, 0xFF},
		{1, 0xFF},
		{-1, 0x7F},
	}
	for _, tc := range zeros {
		if got := LinearToPCMU(tc.sample); got != tc.code {
			t.Errorf("LinearToPCMU(%d) = %#x, quiero %#x", tc.sample, got, tc.code)
		}
	}
	if PCMUToLinear(0xFF) != 0 || PCMUToLinear(0x7F) != 0 {
		t.Fatalf("ceros mu-law: 0xFF=%d 0x7F=%d", PCMUToLinear(0xFF), PCMUToLinear(0x7F))
	}

	extremes := []struct {
		sample int16
		code   byte
		back   int16
	}{
		{32767, 0x80, 32124},
		{32635, 0x80, 32124},
		{32636, 0x80, 32124},
		{32124, 0x80, 32124},
		{-32768, 0x00, -32124},
		{-32767, 0x00, -32124},
		{-32124, 0x00, -32124},
		{8, 0xFE, 8},
		{-8, 0x7E, -8},
	}
	for _, tc := range extremes {
		got := LinearToPCMU(tc.sample)
		if got != tc.code {
			t.Errorf("LinearToPCMU(%d) = %#x, quiero %#x", tc.sample, got, tc.code)
		}
		if back := PCMUToLinear(got); back != tc.back {
			t.Errorf("PCMUToLinear(LinearToPCMU(%d)) = %d, quiero %d", tc.sample, back, tc.back)
		}
	}
	if PCMUToLinear(0x00) != -32124 || PCMUToLinear(0x80) != 32124 {
		t.Fatalf("extremos decodificados: 0x00=%d 0x80=%d", PCMUToLinear(0x00), PCMUToLinear(0x80))
	}
}

func TestMulawDecodeTable(t *testing.T) {
	seen := map[int16]byte{}
	for code := 0; code < 256; code++ {
		got := PCMUToLinear(byte(code))
		want := muLawLinear[code]
		if got != want {
			t.Errorf("PCMUToLinear(%#02x) = %d, quiero %d", code, got, want)
		}
		if prev, ok := seen[got]; ok && got != 0 {
			t.Errorf("nivel %d lo producen %#02x y %#02x", got, prev, code)
		}
		seen[got] = byte(code)
	}
	if seen[0] != 0xFF {
		t.Errorf("el cero queda en el último código positivo %#02x", seen[0])
	}
}

func TestMulawEncodeMatchesReference(t *testing.T) {
	for sample := int32(-32768); sample <= 32767; sample++ {
		got := LinearToPCMU(int16(sample))
		want := referenceLinearToPCMU(int16(sample))
		if got != want {
			t.Fatalf("muestra %d: códec %#x, referencia %#x", sample, got, want)
		}
	}
}

func TestMulawReconstructionPoints(t *testing.T) {
	for code := 0; code < 256; code++ {
		sample := muLawLinear[code]
		got := LinearToPCMU(sample)
		want := byte(code)
		if code == 0x7F {
			want = 0xFF
		}
		if got != want {
			t.Errorf("re-codificar %d (código %#02x) dio %#02x, quiero %#02x", sample, code, got, want)
		}
		pcm := []byte{byte(sample), byte(uint16(sample) >> 8)}
		encoded, err := EncodePCMU(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) != 1 || encoded[0] != got {
			t.Fatalf("EncodePCMU(%d) = %v", sample, encoded)
		}
		decoded := DecodePCMU(encoded)
		back := int16(binary.LittleEndian.Uint16(decoded))
		if back != sample {
			t.Fatalf("DecodePCMU de %d = %d", sample, back)
		}
	}
}

func TestPCM16LEFrame(t *testing.T) {
	if FrameSamples != 320 || PCMUFrameBytes != 320 || PCM16FrameBytes != 640 {
		t.Fatalf("bloque = %d muestras, %d PCMU, %d PCM16", FrameSamples, PCMUFrameBytes, PCM16FrameBytes)
	}
	if SampleRateHz != 8000 || FrameDuration.Milliseconds() != 40 {
		t.Fatalf("8 kHz y 40 ms: rate %d duration %s", SampleRateHz, FrameDuration)
	}
	if got := int64(FrameSamples) * int64(time.Second) / SampleRateHz; got != int64(FrameDuration) {
		t.Fatalf("320/8000 = %d ns, FrameDuration = %d", got, FrameDuration)
	}

	silence := make([]byte, PCM16FrameBytes)
	pcmu, err := EncodePCMU(silence)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePCMUPayload(pcmu); err != nil {
		t.Fatal(err)
	}
	for i, b := range pcmu {
		if b != 0xFF {
			t.Fatalf("silencio[%d] = %#x", i, b)
		}
	}
	back := DecodePCMU(pcmu)
	if len(back) != PCM16FrameBytes {
		t.Fatalf("PCM de vuelta = %d bytes", len(back))
	}
	for i, b := range back {
		if b != 0 {
			t.Fatalf("PCM[%d] = %#x", i, b)
		}
	}

	// Extremos little-endian dentro de un bloque, el resto en cero.
	frame := make([]byte, PCM16FrameBytes)
	binary.LittleEndian.PutUint16(frame[0:], 0x8000)
	binary.LittleEndian.PutUint16(frame[2:], 32767)
	encoded, err := EncodePCMU(frame)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != 0x00 || encoded[1] != 0x80 {
		t.Fatalf("extremos del bloque: %#x %#x", encoded[0], encoded[1])
	}
	if err := ValidatePCMUPayload(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestEncodePCMURejectsOddLength(t *testing.T) {
	_, err := EncodePCMU([]byte{0x00})
	if err == nil {
		t.Fatal("longitud impar aceptada")
	}
	empty, err := EncodePCMU(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("vacío: %v %v", empty, err)
	}
}
