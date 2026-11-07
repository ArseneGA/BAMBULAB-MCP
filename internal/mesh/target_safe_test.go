package mesh

import (
	"strings"
	"testing"
)

func p1sLikeTarget() Target {
	return Target{Name: "Bambu Lab P1S", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4, Source: TargetFromProfile,
		Bed: Rect{0, 0, 256, 256}, Exclusions: []Rect{{0, 0, 18, 28}}}
}

func TestSafeAreaP1S(t *testing.T) {
	s := p1sLikeTarget().SafeArea()
	if s.W() != 238 || s.H() != 228 {
		t.Fatalf("zone sûre %v × %v, attendu 238 × 228", s.W(), s.H())
	}
}

func TestCheckFootprintFieldReports(t *testing.T) {
	tg := p1sLikeTarget()
	// retour terrain : 202×214 passait, 239×141 et 216×236 étaient refusés par Studio (-50)
	c := tg.CheckFootprint(Vec3{202, 214, 20})
	if !c.Fits || !c.FitsSafe || c.Message != "" {
		t.Fatalf("202×214 : %+v", c)
	}
	c = tg.CheckFootprint(Vec3{216, 236, 20})
	if !c.Fits || c.FitsSafe || !c.SafeNeedsRotation {
		t.Fatalf("216×236 : %+v", c)
	}
	for _, d := range []Vec3{{239, 141, 50}} {
		c = tg.CheckFootprint(d)
		if !c.Fits || c.FitsSafe || !strings.Contains(c.Message, "zone utile 238 × 228 mm") || !strings.Contains(c.Message, "zones exclues : x 0–18, y 0–28") {
			t.Fatalf("%v : %+v", d, c)
		}
	}
	c = tg.CheckFootprint(Vec3{300, 100, 20})
	if c.Fits || !strings.Contains(c.Message, "pièce hors zone imprimable : dimensions 300 × 100 × 20 mm") {
		t.Fatalf("300×100 : %+v", c)
	}
	c = tg.CheckFootprint(Vec3{50, 50, 300})
	if !c.TooTall || !strings.Contains(c.Message, "hauteur utile 256 mm") {
		t.Fatalf("hauteur : %+v", c)
	}
}
