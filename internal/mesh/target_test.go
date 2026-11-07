package mesh

import (
	"strings"
	"testing"
)

func TestNominalTargetMarked(t *testing.T) {
	for _, n := range []string{"p1s", "Bambu Lab A1 mini", "a1mini", "H2D", "x1e", "p2s", "h2s", ""} {
		tg, ok := NominalTarget(n)
		if !ok || tg.Source != TargetNominal || len(tg.Notes) == 0 || tg.BedX <= 0 {
			t.Errorf("%q : %+v ok=%v", n, tg, ok)
		}
	}
	if _, ok := NominalTarget("ender3"); ok {
		t.Error("une machine hors Bambu ne doit pas avoir de repli")
	}
	if tg, _ := NominalTarget("a1 mini"); tg.BedX != 180 {
		t.Errorf("A1 mini : %+v", tg)
	}
}

func TestFabricabilityNominalWarning(t *testing.T) {
	tg, _ := NominalTarget("p1s")
	r, err := Fabricability(Cube(10), FabOptions{Target: tg, SkipOrientation: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "nominale") {
			found = true
		}
	}
	if !found {
		t.Errorf("l'avertissement « machine nominale » manque : %v", r.Warnings)
	}
}

func TestFreeRectsCornerExclusion(t *testing.T) {
	tg := Target{BedX: 256, BedY: 256, BedZ: 256, Exclusions: []Rect{{0, 0, 18, 28}}}
	fr := tg.FreeRects()
	if len(fr) != 2 {
		t.Fatalf("2 rectangles libres attendus : %+v", fr)
	}
	w, h := tg.UsableMaxFootprint()
	if w*h < 256*228-1 {
		t.Errorf("plus grande empreinte : %v × %v", w, h)
	}
	if ok, rot := tg.Fits(Vec3{228, 256, 10}); !ok || rot {
		t.Errorf("228×256 : %v %v", ok, rot)
	}
	// 256 × 240 : tient seulement tournée ? non : 256 de large × 240 de profond dépasse la bande 228 et 238.
	if ok, _ := tg.Fits(Vec3{256, 240, 10}); ok {
		t.Error("256×240 ne tient pas")
	}
}

func TestFabricabilityDualNozzleVolumes(t *testing.T) {
	tg := Target{Name: "D2", BedX: 350, BedY: 320, BedZ: 325, Nozzle: 0.4, Extruders: 2, Source: TargetFromProfile,
		Volumes: []NozzleVolume{{1, Rect{0, 0, 325, 320}, 320}, {2, Rect{25, 0, 350, 320}, 325}}}
	r, err := Fabricability(Box(0, 0, 0, 320, 310, 20), FabOptions{Target: tg, SkipOrientation: true})
	if err != nil {
		t.Fatal(err)
	}
	if !r.FitsBed {
		t.Error("320 de large tient avec une seule buse")
	}
	joined := strings.Join(r.Warnings, " | ")
	if !strings.Contains(joined, "zone commune") {
		t.Errorf("l'avertissement de zone commune manque : %v", r.Warnings)
	}
	r, _ = Fabricability(Box(0, 0, 0, 340, 50, 20), FabOptions{Target: tg, SkipOrientation: true})
	if r.FitsBed {
		t.Error("340 de large ne tient dans aucune buse")
	}
}
