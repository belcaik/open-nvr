package pose

import "testing"

func TestCompareRealFrames(t *testing.T) {
	ref, _, err := FromJPEG(readTestFrame(t, "door-day-a.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		verified bool
	}{
		{"door-day-return.jpg", true},
		{"door-day-moved.jpg", false},
	}
	for _, tt := range tests {
		cur, _, err := FromJPEG(readTestFrame(t, tt.name))
		if err != nil {
			t.Fatal(err)
		}
		result := Compare(ref, cur)
		t.Logf("door-day-a.jpg / %s: dx=%d dy=%d cost=%.6f Verified=%t", tt.name, result.Dx, result.Dy, result.Cost, result.Verified)
		if result.Verified != tt.verified {
			t.Errorf("Verified = %t, esperado %t", result.Verified, tt.verified)
		}
	}
}
