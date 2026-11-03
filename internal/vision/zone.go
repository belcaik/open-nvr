package vision

import "math"

// Point es una coordenada normalizada del frame.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Pose limita una zona a una posición PTZ. Cero admite cualquier posición.
type Pose struct {
	Preset int `json:"preset"`
}

// Zone es un polígono configurado por el usuario.
type Zone struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Points []Point `json:"points"`
	Pose   Pose    `json:"pose"`
	Alert  bool    `json:"alert"`
	Zoom   bool    `json:"zoom"`
}

// Contains indica si el punto está dentro del polígono. El borde cuenta.
func (z Zone) Contains(point Point) bool {
	if len(z.Points) < 3 {
		return false
	}
	inside := false
	previous := z.Points[len(z.Points)-1]
	for _, current := range z.Points {
		if pointOnSegment(point, previous, current) {
			return true
		}
		if (current.Y > point.Y) != (previous.Y > point.Y) {
			x := (previous.X-current.X)*(point.Y-current.Y)/(previous.Y-current.Y) + current.X
			if point.X < x {
				inside = !inside
			}
		}
		previous = current
	}
	return inside
}

func pointOnSegment(point, a, b Point) bool {
	const epsilon = 1e-12
	cross := (point.X-a.X)*(b.Y-a.Y) - (point.Y-a.Y)*(b.X-a.X)
	if math.Abs(cross) > epsilon {
		return false
	}
	return point.X >= math.Min(a.X, b.X)-epsilon && point.X <= math.Max(a.X, b.X)+epsilon &&
		point.Y >= math.Min(a.Y, b.Y)-epsilon && point.Y <= math.Max(a.Y, b.Y)+epsilon
}

// Hit comprueba el punto de pies, centro inferior de la caja.
func (z Zone) Hit(box Box) bool {
	return z.Contains(Point{X: (box[0] + box[2]) / 2, Y: box[3]})
}
