package mesh

import (
	"strings"
	"testing"
)

// plateaux : un socle posé sur le plateau et n plateaux horizontaux de côté × côté en l'air (dessous plat = surplomb
// de côté² mm² chacun, une zone par plateau).
func plateaux(n int, side float64) *Mesh {
	m := Box(0, 0, 0, 400, 10, 5)
	for i := 0; i < n; i++ {
		x := float64(i) * (side + 10)
		m.Append(Box(x, 0, 30, x+side, side, 40))
	}
	return m
}

func TestOverhangsForSupportThresholds(t *testing.T) {
	if c := OverhangsForSupport(Cube(40), 30); c.Significant || c.AreaMM2 != 0 {
		t.Fatalf("cube : aucun surplomb attendu : %+v", c)
	}
	big := OverhangsForSupport(plateaux(1, 100), 30)
	if !big.Significant || big.AreaMM2 < 9900 || big.LargestRegionMM2 < 9900 || big.Regions != 1 {
		t.Fatalf("plateau de 100 × 100 : %+v", big)
	}
	cases := []struct {
		name string
		n    int
		side float64
		want bool
	}{
		{"une zone de 100 mm² : pas au-dessus du seuil de zone", 1, 10, false},
		{"une zone de 121 mm² > 100 mm²", 1, 11, true},
		{"deux zones de 81 mm² : 162 mm² cumulés < 200", 2, 9, false},
		{"trois zones de 81 mm² : 243 mm² cumulés > 200", 3, 9, true},
	}
	for _, tc := range cases {
		if c := OverhangsForSupport(plateaux(tc.n, tc.side), 30); c.Significant != tc.want {
			t.Errorf("%s : %+v", tc.name, c)
		}
	}
}

func TestUnsupportedSummaryOnlyWithoutSupports(t *testing.T) {
	c := OverhangsForSupport(plateaux(1, 100), 30)
	if s := c.UnsupportedSummary(true); s != "" {
		t.Fatalf("supports activés : aucun avertissement attendu : %q", s)
	}
	s := c.UnsupportedSummary(false)
	if !strings.Contains(s, "SANS supports") || !strings.Contains(s, "enable_support") {
		t.Fatalf("avertissement attendu : %q", s)
	}
	if (*OverhangSupportCheck)(nil).UnsupportedSummary(false) != "" {
		t.Fatal("nil : aucun avertissement")
	}
}
