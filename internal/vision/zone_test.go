package vision

import "testing"

func concaveZone() Zone {
	return Zone{ID: "l", Points: []Point{
		{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 0.4},
		{X: 0.4, Y: 0.4}, {X: 0.4, Y: 1}, {X: 0, Y: 1},
	}}
}

func TestZoneContainsConcavePolygon(t *testing.T) {
	zone := concaveZone()
	tests := []struct {
		name  string
		point Point
		want  bool
	}{
		{"inside", Point{X: 0.2, Y: 0.8}, true},
		{"outside", Point{X: 0.8, Y: 0.8}, false},
		{"edge", Point{X: 0.4, Y: 0.7}, true},
		{"vertex", Point{X: 0.4, Y: 0.4}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := zone.Contains(test.point); got != test.want {
				t.Fatalf("Contains(%+v) = %v, quiero %v", test.point, got, test.want)
			}
		})
	}
}

func TestZoneHitUsesFeet(t *testing.T) {
	zone := Zone{Points: []Point{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 0.5}, {X: 0, Y: 0.5}}}
	tests := []struct {
		name string
		box  Box
		want bool
	}{
		{"head inside feet outside", Box{0.4, 0.2, 0.6, 0.8}, false},
		{"head outside feet inside", Box{0.4, -0.2, 0.6, 0.3}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := zone.Hit(test.box); got != test.want {
				t.Fatalf("Hit(%v) = %v, quiero %v", test.box, got, test.want)
			}
		})
	}
}
