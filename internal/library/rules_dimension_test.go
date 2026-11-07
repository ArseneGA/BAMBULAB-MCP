package library

import (
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

func dimTemplate() TemplateInfo {
	return TemplateInfo{ID: "t", Params: []ParamSpec{
		{Name: "pin_diameter", Type: "number", Default: 3.0},
		{Name: "hole_clearance", Type: "number", Default: 0.3, Auto: "fit:glissant:pin_diameter"},
		{Name: "plain_clearance", Type: "number", Default: 0.3, Auto: "fit:glissant"},
	}}
}

func TestFitRuleUsesNominalDimensionOfAnotherParam(t *testing.T) {
	r := &resolver{nozzle: 0.4, compensate: true}
	// diamètre par défaut du gabarit (3 mm) : jeu plus petit que la référence de 10 mm
	params, autos, calcs, _, err := r.apply(dimTemplate(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	ref := params["plain_clearance"].(float64)
	near(t, ref, 0.25, 1e-9, "sans dimension : référence 10 mm")
	near(t, params["hole_clearance"].(float64), 0.25+engineering.FitDimensionDelta(3), 1e-9, "axe de 3 mm")
	if params["hole_clearance"].(float64) >= ref {
		t.Fatal("un petit axe doit avoir un jeu plus serré que la référence")
	}
	if len(autos) != 2 || calcs[0].Extra["dimension_mm"] != 3.0 {
		t.Fatalf("dimension absente du calcul : %+v", calcs[0].Extra)
	}
	// diamètre fourni : le jeu suit la valeur fournie
	params, _, _, _, err = r.apply(dimTemplate(), map[string]any{"pin_diameter": 40.0})
	if err != nil {
		t.Fatal(err)
	}
	near(t, params["hole_clearance"].(float64), 0.25+engineering.FitDimensionDelta(40), 1e-9, "axe de 40 mm")
	if params["hole_clearance"].(float64) <= ref {
		t.Fatal("un gros axe doit avoir un jeu plus large")
	}
	// hors du domaine 3-100 mm : ramené au bord avec avertissement (jamais d'erreur)
	params, _, _, warns, err := r.apply(dimTemplate(), map[string]any{"pin_diameter": 250.0})
	if err != nil {
		t.Fatal(err)
	}
	near(t, params["hole_clearance"].(float64), 0.25+engineering.FitDimensionDelta(100), 1e-9, "axe de 250 mm plafonné à 100")
	if len(warns) != 1 || !strings.Contains(warns[0], "100 mm") {
		t.Fatalf("avertissement : %v", warns)
	}
}

func TestFitRuleWithCalibrationScalesFromTenMillimetres(t *testing.T) {
	slides := 0.32
	r := &resolver{nozzle: 0.4, compensate: true, cal: &engineering.Calibration{Provenance: "calibré le test", SlidesGapMM: &slides}}
	params, autos, _, _, err := r.apply(dimTemplate(), map[string]any{"pin_diameter": 10.0})
	if err != nil {
		t.Fatal(err)
	}
	near(t, params["hole_clearance"].(float64), 0.32, 1e-9, "à 10 mm : le jeu mesuré tel quel")
	if autos[0].Level != "measured" {
		t.Fatalf("niveau : %+v", autos[0])
	}
	params, autos, _, _, _ = r.apply(dimTemplate(), map[string]any{"pin_diameter": 60.0})
	near(t, params["hole_clearance"].(float64), 0.32+engineering.FitDimensionDelta(60), 1e-9, "à 60 mm")
	if autos[0].Level != "calculated" {
		t.Fatalf("extrapolé : niveau attendu calculated, %+v", autos[0])
	}
}
