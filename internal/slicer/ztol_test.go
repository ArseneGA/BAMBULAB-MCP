package slicer

import "testing"

func TestZTolerance(t *testing.T) {
	cases := []struct{ layer, first, want float64 }{
		{0.2, 0.2, 0.25}, {0.08, 0.2, 0.25}, {0.28, 0.2, 0.28}, {0.12, 0.35, 0.35}, {0, 0, 0.25},
	}
	for _, c := range cases {
		if got := ZTolerance(c.layer, c.first); got != c.want {
			t.Errorf("ZTolerance(%v,%v)=%v, attendu %v", c.layer, c.first, got, c.want)
		}
	}
	// cas terrain : trajectoire 203,84 pour 203,96 annoncé = 0,12 mm d'écart, accepté avec une couche de 0,2 mm
	if d := 203.96 - 203.84; d > ZTolerance(0.2, 0.2) {
		t.Fatalf("0,12 mm refusé alors que la tolérance est %v", ZTolerance(0.2, 0.2))
	}
}
