package pose

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestFromJPEGModesAndSize(t *testing.T) {
	tests := []struct {
		name string
		mode Mode
	}{
		{"door-day-a.jpg", Day},
		{"door-day-return.jpg", Day},
		{"door-day-moved.jpg", Day},
		{"yard-night-ir.jpg", Night},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := readTestFrame(t, tt.name)
			g, mode, err := FromJPEG(data)
			if err != nil {
				t.Fatal(err)
			}
			if g.W != 160 || g.H != 90 || len(g.Pix) != 160*90 {
				t.Fatalf("tamaño = %dx%d, %d píxeles", g.W, g.H, len(g.Pix))
			}
			if mode != tt.mode {
				t.Fatalf("modo = %s, esperado %s", mode, tt.mode)
			}
			img, err := jpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s saturation=%.6f", tt.name, meanSaturation(img))
		})
	}
}

func TestFromJPEGIgnoresOSD(t *testing.T) {
	data := readTestFrame(t, "door-day-a.jpg")
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	original := image.NewRGBA(b)
	draw.Draw(original, b, img, b.Min, draw.Src)
	changed := image.NewRGBA(b)
	draw.Draw(changed, b, img, b.Min, draw.Src)
	y := b.Min.Y + 88*b.Dy()/100
	draw.Draw(changed, image.Rect(b.Min.X, y, b.Max.X, b.Max.Y), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	var encodedOriginal, encodedChanged bytes.Buffer
	if err := jpeg.Encode(&encodedOriginal, original, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&encodedChanged, changed, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	a, _, err := FromJPEG(encodedOriginal.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := FromJPEG(encodedChanged.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result := Compare(a, c)
	t.Logf("coste tras cambiar sólo el OSD: %.6f", result.Cost)
	if result.Cost > 0.5 {
		t.Fatalf("el OSD afectó al coste: %.4f", result.Cost)
	}
}

func readTestFrame(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
