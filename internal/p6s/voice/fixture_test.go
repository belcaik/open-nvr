package voice_test

import (
	"testing"

	"github.com/belcaik/open-nvr/internal/p6s/voice"
)

func TestCapturedAudioExtension(t *testing.T) {
	extension, err := voice.CapturedAudioExtension()
	if err != nil {
		t.Fatal(err)
	}
	if len(extension.Bytes()) != voice.AudioExtensionLen {
		t.Fatalf("extensión = %d bytes", len(extension.Bytes()))
	}
	if extension.Bytes()[31] != 3 {
		t.Fatalf("fixture inesperado: %x", extension.Bytes())
	}
}
