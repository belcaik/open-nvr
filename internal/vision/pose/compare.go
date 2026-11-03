package pose

import "math"

const (
	maxCompareShift = 16

	// Calibrado con door tras normalizar: return cost=8,28, moved=31,69 y
	// return oscurecido al 60 % = 8,30. El umbral queda entre ambos.
	MaxCost = 20.0
)

// Result contiene la alineación y el resultado de la comparación.
type Result struct {
	Dx, Dy   int
	Cost     float64
	Verified bool
}

// Compare comprueba si dos vistas corresponden al mismo preset. Antes
// iguala brillo y contraste: la referencia puede ser de otra hora del día.
func Compare(ref, cur Gray) Result {
	dx, dy, cost := EstimateShift(normalize(ref), normalize(cur), maxCompareShift)
	return Result{
		Dx:       dx,
		Dy:       dy,
		Cost:     cost,
		Verified: abs(dx) <= 4 && abs(dy) <= 4 && cost <= MaxCost,
	}
}

// normalize lleva la imagen a media 128 y desviación 40.
func normalize(g Gray) Gray {
	if len(g.Pix) == 0 {
		return g
	}
	var sum, sq float64
	for _, p := range g.Pix {
		v := float64(p)
		sum += v
		sq += v * v
	}
	n := float64(len(g.Pix))
	mean := sum / n
	std := math.Sqrt(math.Max(sq/n-mean*mean, 0))
	if std < 1 {
		std = 1
	}
	out := Gray{W: g.W, H: g.H, Pix: make([]byte, len(g.Pix))}
	for i, p := range g.Pix {
		v := 128 + (float64(p)-mean)*40/std
		out.Pix[i] = byte(math.Max(0, math.Min(255, math.Round(v))))
	}
	return out
}
