package voice

import _ "embed"

//go:embed testdata/client-audio-extension.bin
var capturedAudioExtension []byte

func CapturedAudioExtension() (AudioExtension, error) {
	return ParseAudioExtension(capturedAudioExtension)
}
