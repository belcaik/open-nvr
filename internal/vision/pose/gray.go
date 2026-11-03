// Package pose verifica visualmente la posición de una cámara PTZ.
package pose

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

const (
	grayWidth  = 160
	grayHeight = 90
	osdStart   = 79 // El 12 % inferior puede contener fecha y hora.

	// Saturación media medida: door a=0,229369, return=0,229505,
	// moved=0,164344 y yard IR=0,000000. El margen separa el IR.
	nightSaturation = 0.10
)

// Mode distingue referencias diurnas y nocturnas.
type Mode uint8

const (
	Day Mode = iota
	Night
)

func (m Mode) String() string {
	if m == Night {
		return "night"
	}
	return "day"
}

// Gray es una imagen de luminancia con píxeles en orden de filas.
type Gray struct {
	W, H int
	Pix  []byte
}

// FromJPEG decodifica y reduce un frame a 160x90.
func FromJPEG(data []byte) (Gray, Mode, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return Gray{}, Day, fmt.Errorf("pose: decodificar JPEG: %w", err)
	}

	mode := Day
	if meanSaturation(img) < nightSaturation {
		mode = Night
	}
	g := Gray{W: grayWidth, H: grayHeight, Pix: make([]byte, grayWidth*grayHeight)}
	b := img.Bounds()
	for oy := 0; oy < osdStart; oy++ {
		sy0 := b.Min.Y + oy*b.Dy()/grayHeight
		sy1 := b.Min.Y + (oy+1)*b.Dy()/grayHeight
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for ox := 0; ox < grayWidth; ox++ {
			sx0 := b.Min.X + ox*b.Dx()/grayWidth
			sx1 := b.Min.X + (ox+1)*b.Dx()/grayWidth
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var sum uint64
			for y := sy0; y < sy1; y++ {
				for x := sx0; x < sx1; x++ {
					r, gg, bb, _ := img.At(x, y).RGBA()
					sum += uint64((299*r + 587*gg + 114*bb + 500) / 1000 >> 8)
				}
			}
			g.Pix[oy*grayWidth+ox] = byte(sum / uint64((sx1-sx0)*(sy1-sy0)))
		}
	}
	return g, mode, nil
}

func meanSaturation(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			maxC := max(r, g, bl)
			minC := min(r, g, bl)
			if maxC != 0 {
				sum += float64(maxC-minC) / float64(maxC)
			}
		}
	}
	return sum / float64(b.Dx()*b.Dy())
}
