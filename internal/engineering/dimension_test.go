package engineering

import (
	"strings"
	"testing"
)

func TestFitDimensionDeltaShape(t *testing.T) {
	if d := FitDimensionDelta(FitRefDimensionMM); d != 0 {
		t.Fatalf("dimension de référence : %v", d)
	}
	// croissance monotone, négative en dessous de 10 mm, positive au-dessus
	prev := FitDimensionDelta(3)
	if prev >= 0 {
		t.Fatalf("3 mm : %v", prev)
	}
	for _, d := range []float64{5, 10, 20, 40, 70, 100} {
		v := FitDimensionDelta(d)
		if v < prev {
			t.Fatalf("non monotone à %v mm : %v < %v", d, v, prev)
		}
		prev = v
	}
	// ordres de grandeur documentés
	near(t, "3 mm", FitDimensionDelta(3), -0.039, 0.002)
	near(t, "30 mm", FitDimensionDelta(30), 0.054, 0.003)
	near(t, "100 mm", FitDimensionDelta(100), 0.145, 0.003)
	if FitDimensionDelta(0) != 0 {
		t.Fatal("dimension nulle")
	}
}

func TestFitClearanceGrowsWithDimension(t *testing.T) {
	base, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical"})
	ref, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: 10})
	small, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: 4})
	big, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: 80})
	near(t, "sans dimension = référence 10 mm", base.Value, ref.Value, 1e-9)
	if !(small.Value < ref.Value && ref.Value < big.Value) {
		t.Fatalf("jeu non croissant : %v %v %v", small.Value, ref.Value, big.Value)
	}
	near(t, "80 mm", big.Value, 0.25+FitDimensionDelta(80), 1e-9)
	if big.Level != LevelHeuristic || !containsAny(big.Assumptions, "ISO 286-1") {
		t.Fatalf("la loi doit être marquée heuristique et sourcée : %v %v", big.Level, big.Assumptions)
	}
	if !containsAny(base.Assumptions, "Dimension non fournie") {
		t.Fatalf("hypothèse de dimension de référence manquante : %v", base.Assumptions)
	}
}

func TestFitClearanceCalScalesFromTenMillimetres(t *testing.T) {
	at10, _ := FitClearanceCal(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: 10}, testCal)
	at50, _ := FitClearanceCal(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: 50}, testCal)
	near(t, "à 10 mm : le jeu mesuré tel quel", at10.Value, 0.32, 1e-9)
	near(t, "à 50 mm", at50.Value, 0.32+FitDimensionDelta(50), 1e-9)
	if at10.Level != LevelMeasured || at50.Level != LevelCalculated {
		t.Fatalf("niveaux : %v %v", at10.Level, at50.Level)
	}
	if !strings.Contains(strings.Join(at50.Assumptions, " "), "Dimension 50 mm") {
		t.Fatalf("hypothèses : %v", at50.Assumptions)
	}
}

func TestNozzleTempRangeAndSeries(t *testing.T) {
	cases := []struct {
		mat    string
		lo, hi float64
	}{{"PLA", 190, 240}, {"pla", 190, 240}, {"PETG-HF", 220, 260}, {"PETG", 220, 260}, {"ABS", 240, 280}, {"ASA", 240, 280},
		{"TPU-95A", 200, 250}, {"PC", 260, 290}, {"PA-CF", 260, 300}, {"PLA-CF", 190, 240}, {"PETG-CF", 220, 260}}
	for _, c := range cases {
		lo, hi, src, ok := NozzleTempRange(c.mat)
		if !ok || lo != c.lo || hi != c.hi || src.ID == "" {
			t.Errorf("%s : %v %v %v %v", c.mat, lo, hi, ok, src)
		}
	}
	if _, _, _, ok := NozzleTempRange("matière-mystère"); ok {
		t.Error("matière inconnue acceptée")
	}
	got := TemperatureSeries(220, 260, 5)
	want := []float64{220, 230, 240, 250, 260}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
	if s := TemperatureSeries(190, 240, 5); s[0] != 190 || s[len(s)-1] != 240 || len(s) != 5 {
		t.Fatalf("%v", s)
	}
	if s := TemperatureSeries(200, 200, 5); len(s) != 1 {
		t.Fatalf("plage nulle : %v", s)
	}
}
