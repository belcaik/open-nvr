package voice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	// SampleRateHz es la frecuencia del audio de voz P6S.
	SampleRateHz = 8000
	// FrameSamples es un bloque de red: 320 muestras.
	FrameSamples = 320
	// PCMUFrameBytes es ese bloque en G.711 mu-law: un byte por muestra.
	PCMUFrameBytes = 320
	// PCM16FrameBytes es el mismo bloque en PCM mono S16LE.
	PCM16FrameBytes = 640
	// FrameDuration es 320 muestras a 8 kHz.
	FrameDuration = 40 * time.Millisecond
	// AudioExtensionLen es el tamaño observado de la extensión de audio.
	// El contenido es opaco: esta capa no interpreta campos ni offsets.
	AudioExtensionLen = 44
)

// ErrPCMULength indica que la carga no es un bloque PCMU de 320 bytes.
var ErrPCMULength = errors.New("longitud PCMU")

// ErrAudioExtension indica que la extensión no mide 44 bytes.
var ErrAudioExtension = errors.New("extensión de audio")

// ErrOddPCM indica que el PCM S16LE no cierra en una muestra.
var ErrOddPCM = errors.New("PCM S16LE incompleto")

// AudioExtension es la extensión de 44 bytes que acompaña a un bloque de
// audio. Se conserva tal cual: no se leen marcas de tiempo ni longitudes.
type AudioExtension [AudioExtensionLen]byte

// ParseAudioExtension exige exactamente 44 bytes. No recorta ni rellena.
func ParseAudioExtension(b []byte) (AudioExtension, error) {
	if len(b) != AudioExtensionLen {
		return AudioExtension{}, fmt.Errorf("voice: %w: se esperaban %d bytes y hay %d", ErrAudioExtension, AudioExtensionLen, len(b))
	}
	var ext AudioExtension
	copy(ext[:], b)
	return ext, nil
}

// Bytes devuelve una copia de los 44 bytes.
func (e AudioExtension) Bytes() []byte {
	out := make([]byte, AudioExtensionLen)
	copy(out, e[:])
	return out
}

// ValidatePCMUPayload acepta solo la carga de audio: 320 bytes PCMU, que son
// 320 muestras y 40 ms a 8 kHz. No interpreta ni retira un encabezado TCP.
func ValidatePCMUPayload(payload []byte) error {
	if len(payload) != PCMUFrameBytes {
		return fmt.Errorf("voice: %w: se esperaban %d bytes (320 muestras, 40 ms a 8 kHz) y hay %d", ErrPCMULength, PCMUFrameBytes, len(payload))
	}
	return nil
}

// EncodePCMU convierte PCM mono S16LE a PCMU. Cada muestra little-endian
// produce un byte. La longitud de entrada tiene que ser par. Un bloque de
// red son 640 bytes de entrada y 320 de salida; esta función también acepta
// otras longitudes pares.
func EncodePCMU(pcmS16LE []byte) ([]byte, error) {
	if len(pcmS16LE)%2 != 0 {
		return nil, fmt.Errorf("voice: %w: %d bytes", ErrOddPCM, len(pcmS16LE))
	}
	out := make([]byte, len(pcmS16LE)/2)
	for i := range out {
		sample := int16(binary.LittleEndian.Uint16(pcmS16LE[i*2 : i*2+2]))
		out[i] = LinearToPCMU(sample)
	}
	return out, nil
}

// DecodePCMU convierte PCMU a PCM mono S16LE. Cada byte produce dos bytes
// little-endian. No comprueba el tamaño de bloque de red.
func DecodePCMU(pcmu []byte) []byte {
	out := make([]byte, len(pcmu)*2)
	for i, code := range pcmu {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(PCMUToLinear(code)))
	}
	return out
}
