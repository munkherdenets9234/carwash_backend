package geo

import (
	"math"
	"testing"
)

func TestDistanceM(t *testing.T) {
	// Sukhbaatar Square, Ulaanbaatar.
	const lat, lng = 47.918730, 106.917700

	t.Run("same point is zero", func(t *testing.T) {
		if got := DistanceM(lat, lng, lat, lng); got != 0 {
			t.Fatalf("want 0, got %v", got)
		}
	})

	t.Run("one degree of latitude is about 111 km", func(t *testing.T) {
		got := DistanceM(lat, lng, lat+1, lng)
		if math.Abs(got-111195) > 200 {
			t.Fatalf("want ~111195 m, got %.0f", got)
		}
	})

	t.Run("short offsets are metre-accurate", func(t *testing.T) {
		// ~0.0009 degrees of latitude is very close to 100 m anywhere.
		got := DistanceM(lat, lng, lat+0.0009, lng)
		if math.Abs(got-100) > 2 {
			t.Fatalf("want ~100 m, got %.1f", got)
		}
	})

	t.Run("is symmetric", func(t *testing.T) {
		a := DistanceM(lat, lng, lat+0.01, lng+0.01)
		b := DistanceM(lat+0.01, lng+0.01, lat, lng)
		if math.Abs(a-b) > 1e-9 {
			t.Fatalf("asymmetric: %v vs %v", a, b)
		}
	})
}

func TestValidCoordinate(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     bool
	}{
		{"ulaanbaatar", 47.91873, 106.9177, true},
		{"southern hemisphere", -33.86, 151.21, true},
		{"null island is treated as no fix", 0, 0, false},
		{"latitude out of range", 91, 10, false},
		{"longitude out of range", 10, 181, false},
		{"NaN", math.NaN(), 10, false},
		{"Inf", math.Inf(1), 10, false},
		// A real coordinate that happens to have one zero component must
		// still pass — only the exact (0,0) pair is rejected.
		{"zero latitude only", 0, 106.9177, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidCoordinate(c.lat, c.lng); got != c.want {
				t.Fatalf("ValidCoordinate(%v,%v) = %v, want %v", c.lat, c.lng, got, c.want)
			}
		})
	}
}
