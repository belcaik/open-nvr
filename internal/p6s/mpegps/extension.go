// Package mpegps reconstruye MPEG-PS transportado en tramas de vídeo P6S.
package mpegps

import (
	"errors"
	"fmt"
)

const VideoExtensionLen = 44

var ErrVideoExtension = errors.New("extensión de vídeo P6S")

// VideoExtension conserva la extensión P6S sin interpretar sus campos.
type VideoExtension [VideoExtensionLen]byte

func ParseVideoExtension(b []byte) (VideoExtension, error) {
	if len(b) != VideoExtensionLen {
		return VideoExtension{}, fmt.Errorf("mpegps: %w: se esperaban %d bytes y hay %d", ErrVideoExtension, VideoExtensionLen, len(b))
	}
	var extension VideoExtension
	copy(extension[:], b)
	return extension, nil
}

func (e VideoExtension) Bytes() []byte {
	out := make([]byte, VideoExtensionLen)
	copy(out, e[:])
	return out
}
