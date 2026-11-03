package pose

import (
	"math"
	"testing"
)

func TestEstimateShift(t *testing.T) {
	const w, h = 43, 31
	a := Gray{W: w, H: h, Pix: make([]byte, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a.Pix[y*w+x] = byte((x*31 + y*17 + x*y*3) % 251)
		}
	}
	for _, shift := range []struct{ dx, dy int }{{0, 0}, {5, -3}, {-6, 4}} {
		b := Gray{W: w, H: h, Pix: make([]byte, w*h)}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sx, sy := x-shift.dx, y-shift.dy
				if sx >= 0 && sx < w && sy >= 0 && sy < h {
					b.Pix[y*w+x] = a.Pix[sy*w+sx]
				}
			}
		}
		dx, dy, cost := EstimateShift(a, b, 8)
		if dx != shift.dx || dy != shift.dy || cost != 0 {
			t.Errorf("shift (%d,%d): obtenido (%d,%d), cost %.3f", shift.dx, shift.dy, dx, dy, cost)
		}
	}
}

func TestEstimateShiftInvalid(t *testing.T) {
	_, _, cost := EstimateShift(Gray{}, Gray{}, 2)
	if !math.IsInf(cost, 1) {
		t.Fatalf("cost = %v, esperado +Inf", cost)
	}
}
