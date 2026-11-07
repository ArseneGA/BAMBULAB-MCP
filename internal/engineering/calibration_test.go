package engineering

import (
	"math"
	"strings"
	"testing"
)

func fp(v float64) *float64 { return &v }

func approx(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s : %v, attendu %v", what, got, want)
	}
}

var testCal = &Calibration{
	Provenance: "calibré le 2026-11-07 (P1S, buse 0.4, PLA)", CalibratedAt: "2026-11-07",
	SlidesGapMM: fp(0.32), SnugGapMM: fp(0.18),
	HoleVertical: &LinearOffset{Offset: 0.1, Slope: 0.01, N: 6}, Shaft: &LinearOffset{Offset: -0.05, N: 4},
	ShrinkXY: fp(1.003), ShrinkZ: fp(1.001),
}

func TestFitClearanceCalOverridesGeneric(t *testing.T) {
	gen, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical"})
	approx(t, gen.Value, 0.25, "générique glissant")
	cases := []struct {
		fit  FitType
		want float64
	}{{FitSliding, 0.32}, {FitFree, 0.47}, {FitSnug, 0.18}}
	for _, c := range cases {
		r, err := FitClearanceCal(FitParams{Fit: c.fit, Nozzle: 0.4, Orientation: "vertical"}, testCal)
		if err != nil {
			t.Fatal(err)
		}
		approx(t, r.Value, c.want, string(c.fit))
		if r.Level != LevelMeasured || len(r.Sources) != 1 || r.Sources[0].ID != "calibration" || !strings.Contains(r.Assumptions[0], "calibré le 2026-11-07") {
			t.Fatalf("provenance : %+v", r.Result)
		}
		approx(t, r.PerSide, math.Round(c.want/2*1e4)/1e4, "par côté")
	}
	// orientation horizontale : +0,05
	h, _ := FitClearanceCal(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "horizontal"}, testCal)
	approx(t, h.Value, 0.37, "horizontal")
	// serré non mesuré : dérivé du glissant, niveau « calculated »
	c2 := *testCal
	c2.SnugGapMM = nil
	s, _ := FitClearanceCal(FitParams{Fit: FitSnug, Nozzle: 0.4, Orientation: "vertical"}, &c2)
	approx(t, s.Value, 0.17, "serré dérivé")
	if s.Level != LevelCalculated {
		t.Fatalf("niveau : %v", s.Level)
	}
	// press : jamais calibré
	p, _ := FitClearanceCal(FitParams{Fit: FitPress, Nozzle: 0.4, Orientation: "vertical"}, testCal)
	approx(t, p.Value, -0.10, "press générique")
	// sans calibration ou sans jeu mesuré : identique au générique
	for _, c := range []*Calibration{nil, {Provenance: "x"}} {
		g, _ := FitClearanceCal(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical"}, c)
		approx(t, g.Value, 0.25, "repli générique")
		if g.Level == LevelMeasured {
			t.Fatal("niveau measured sans calibration")
		}
	}
	// les entrées invalides restent refusées
	if _, err := FitClearanceCal(FitParams{Fit: "inconnu", Nozzle: 0.4, Orientation: "vertical"}, testCal); err == nil {
		t.Fatal("ajustement inconnu accepté")
	}
}

func TestHoleCompensationCal(t *testing.T) {
	r, err := HoleCompensationCal(HoleParams{Diameter: 6, Axis: "vertical", Nozzle: 0.4, Material: "PLA"}, testCal)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, r.Value, 6+0.1+0.01*6, "droite de calibration")
	if r.Level != LevelMeasured || !strings.Contains(r.Assumptions[0], "calibré le") {
		t.Fatalf("%+v", r)
	}
	// horizontal non mesuré : vertical + 0,10, niveau calculated
	h, _ := HoleCompensationCal(HoleParams{Diameter: 6, Axis: "horizontal", Nozzle: 0.4}, testCal)
	approx(t, h.Value, 6.26, "horizontal dérivé")
	if h.Level != LevelCalculated {
		t.Fatalf("%v", h.Level)
	}
	// hors plage testée : avertissement
	big, _ := HoleCompensationCal(HoleParams{Diameter: 40, Axis: "vertical", Nozzle: 0.4}, testCal)
	if len(big.Warnings) == 0 {
		t.Fatal("extrapolation non signalée")
	}
	// sans calibration : générique
	g, _ := HoleCompensationCal(HoleParams{Diameter: 6, Axis: "vertical", Nozzle: 0.4}, nil)
	gen, _ := HoleCompensation(HoleParams{Diameter: 6, Axis: "vertical", Nozzle: 0.4})
	approx(t, g.Value, gen.Value, "générique")
	if _, err := HoleCompensationCal(HoleParams{Diameter: 0, Axis: "vertical", Nozzle: 0.4}, testCal); err == nil {
		t.Fatal("diamètre nul accepté")
	}
}

func TestShaftAndScaleCal(t *testing.T) {
	s, err := ShaftCompensationCal(8, testCal)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, s.Value, 7.95, "arbre")
	g, _ := ShaftCompensationCal(8, nil)
	approx(t, g.Value, 8, "arbre sans calibration")
	xy, z, lvl, note, err := ScaleCal("PLA", testCal)
	if err != nil || xy != 1.003 || z != 1.001 || lvl != LevelMeasured || !strings.Contains(note, "calibré le") {
		t.Fatalf("%v %v %v %v %v", xy, z, lvl, note, err)
	}
	xy, z, lvl, _, err = ScaleCal("PLA", nil)
	if err != nil || xy <= 1 || xy != z || lvl == LevelMeasured {
		t.Fatalf("retrait générique : %v %v %v %v", xy, z, lvl, err)
	}
	xy, z, _, _, err = ScaleCal("", nil)
	if err != nil || xy != 1 || z != 1 {
		t.Fatalf("%v %v %v", xy, z, err)
	}
}
