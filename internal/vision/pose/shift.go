package pose

import "math"

// EstimateShift devuelve el desplazamiento de b respecto de a mediante SAD.
func EstimateShift(a, b Gray, maxShift int) (dx, dy int, cost float64) {
	bestCost := math.Inf(1)
	if a.W <= 0 || a.H <= 0 || a.W != b.W || a.H != b.H || maxShift < 0 ||
		len(a.Pix) < a.W*a.H || len(b.Pix) < b.W*b.H {
		return 0, 0, bestCost
	}
	limitX := min(maxShift, a.W-1)
	limitY := min(maxShift, a.H-1)
	bestDX, bestDY := 0, 0
	for sy := -limitY; sy <= limitY; sy++ {
		for sx := -limitX; sx <= limitX; sx++ {
			x0, x1 := max(0, -sx), min(a.W, a.W-sx)
			y0, y1 := max(0, -sy), min(a.H, a.H-sy)
			var sad uint64
			for y := y0; y < y1; y++ {
				aoff := y*a.W + x0
				boff := (y+sy)*a.W + x0 + sx
				for x := x0; x < x1; x++ {
					sad += uint64(abs(int(a.Pix[aoff]) - int(b.Pix[boff])))
					aoff++
					boff++
				}
			}
			mean := float64(sad) / float64((x1-x0)*(y1-y0))
			if mean < bestCost || mean == bestCost && abs(sx)+abs(sy) < abs(bestDX)+abs(bestDY) {
				bestCost, bestDX, bestDY = mean, sx, sy
			}
		}
	}
	return bestDX, bestDY, bestCost
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
