package engineering

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, attendu %.6f (±%g)", name, got, want, tol)
	}
}

func TestMaterialsLoad(t *testing.T) {
	d, err := Materials()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"PLA", "PETG", "ABS", "ASA", "TPU-95A", "PA", "PA-CF", "PC", "PLA-CF", "PETG-CF", "PVA"} {
		m, err := GetMaterial(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for k, p := range m.Properties {
			if p.Source == "" || p.Date == "" || p.Level == "" || p.Domain == "" {
				t.Errorf("%s.%s: provenance incomplète %+v", id, k, p)
			}
			if _, ok := d.Sources[p.Source]; !ok {
				t.Errorf("%s.%s: source %q absente", id, k, p.Source)
			}
		}
	}
	if _, err := GetMaterial("inconnu"); !errors.Is(err, ErrInvalidInput) {
		t.Error("matériau inconnu doit échouer")
	}
	pla, _ := GetMaterial("pla")
	tz, _ := pla.Prop("tensile_z")
	if *tz.Value != 35 || tz.Level != LevelMeasured {
		t.Errorf("PLA tensile_z = %+v", tz)
	}
	if !strings.HasPrefix(d.Sources["bambu_pla_basic"].URL, "https://") {
		t.Error("URL source TDS manquante")
	}
	pva, _ := GetMaterial("PVA")
	if _, ok := pva.Prop("tensile_xy"); ok {
		t.Error("PVA tensile doit être null")
	}
}

func TestBeamCantileverRect(t *testing.T) {
	// Référence : E=2750 (flexion PLA XY), rect 20x10, L=100, F=10 N.
	r, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PLA", Orientation: "xy"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "I", r.InertiaMM4, 1666.6667, 1e-3)
	near(t, "M", r.MomentNmm, 1000, 1e-9)
	near(t, "sigma", r.Value, 3.0, 1e-9)
	near(t, "flèche", r.DeflectionMM, 0.72727, 1e-4)
	near(t, "SF", r.SafetyFactor, 25.3333, 1e-3)
	near(t, "SF_xy", *r.SafetyFactorXY, 25.3333, 1e-3)
	near(t, "SF_z", *r.SafetyFactorZ, 22.6667, 1e-3)
	if r.Verdict != "acceptable" || r.Level != LevelCalculated {
		t.Errorf("verdict/level = %s/%s", r.Verdict, r.Level)
	}
	if len(r.Sources) == 0 || r.Sources[0].URL == "" {
		t.Error("source TDS attendue")
	}
}

func TestBeamSimpleCircle(t *testing.T) {
	r, err := BeamStress(BeamParams{Support: "simple", LoadType: "point", Load: 50, Length: 100,
		Section: Section{Shape: "circle", D: 10}, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "I", r.InertiaMM4, 490.8739, 1e-3)
	near(t, "M", r.MomentNmm, 1250, 1e-9)
	near(t, "sigma", r.Value, 12.7324, 1e-3)
	near(t, "flèche", r.DeflectionMM, 0.77166, 1e-4)
	near(t, "SF_xy", *r.SafetyFactorXY, 5.969, 1e-3)
}

func TestBeamDistributedAndTube(t *testing.T) {
	// Console répartie : M=wL²/2, δ=wL⁴/(8EI). w=0.1 N/mm, L=100, rect 10x5.
	r, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "distributed", Load: 0.1, Length: 100,
		Section: Section{Shape: "rect", B: 10, H: 5}, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "M", r.MomentNmm, 500, 1e-9)
	I := 10 * 125.0 / 12
	near(t, "δ", r.DeflectionMM, 0.1*1e8/(8*2750*I), 1e-9)
	// Appuis simples répartie : 5wL⁴/(384EI)
	r2, _ := BeamStress(BeamParams{Support: "simple", LoadType: "distributed", Load: 0.1, Length: 100,
		Section: Section{Shape: "rect", B: 10, H: 5}, Material: "PLA"})
	near(t, "δ simple", r2.DeflectionMM, 5*0.1*1e8/(384*2750*I), 1e-9)
	near(t, "M simple", r2.MomentNmm, 0.1*1e4/8, 1e-9)
	// Tube
	r3, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 5, Length: 50,
		Section: Section{Shape: "tube", Do: 10, Di: 6}, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "I tube", r3.InertiaMM4, math.Pi*(1e4-1296)/64, 1e-9)
}

func TestBeamZWarnsAndFallback(t *testing.T) {
	r, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PLA", Orientation: "z"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "SF z", r.SafetyFactor, 22.6667, 1e-3)
	if !containsAny(r.Warnings, "perpendiculaire aux couches") {
		t.Errorf("avertissement Z manquant: %v", r.Warnings)
	}
	// PA : résistance Z indicative connue, E Z indicatif connu → niveau heuristique
	pa, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PA"})
	if err != nil {
		t.Fatal(err)
	}
	if pa.Level != LevelHeuristic {
		t.Errorf("PA level = %s", pa.Level)
	}
	// Poutre courte et échec
	f, _ := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 800, Length: 20,
		Section: Section{Shape: "rect", B: 10, H: 10}, Material: "PLA"})
	if f.Verdict != "fail" || !containsAny(f.Warnings, "L/h < 5") {
		t.Errorf("fail attendu: %s %v", f.Verdict, f.Warnings)
	}
}

func TestBeamInvalid(t *testing.T) {
	base := BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PLA"}
	cases := map[string]func(*BeamParams){
		"L=0":        func(p *BeamParams) { p.Length = 0 },
		"F<0":        func(p *BeamParams) { p.Load = -1 },
		"appui":      func(p *BeamParams) { p.Support = "x" },
		"charge":     func(p *BeamParams) { p.LoadType = "x" },
		"section":    func(p *BeamParams) { p.Section.Shape = "x" },
		"b=0":        func(p *BeamParams) { p.Section.B = 0 },
		"tube Di>Do": func(p *BeamParams) { p.Section = Section{Shape: "tube", Do: 5, Di: 6} },
		"matériau":   func(p *BeamParams) { p.Material = "?" },
		"PVA null":   func(p *BeamParams) { p.Material = "PVA" },
		"orient":     func(p *BeamParams) { p.Orientation = "q" },
		"NaN":        func(p *BeamParams) { p.Load = math.NaN() },
	}
	for name, mod := range cases {
		p := base
		mod(&p)
		if _, err := BeamStress(p); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: erreur attendue, reçu %v", name, err)
		}
	}
}

func TestSnapFit(t *testing.T) {
	r, err := SnapFit(SnapParams{Length: 10, Thickness: 2, Width: 5, Deflection: 1, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "ε", r.Value, 0.03, 1e-12)
	if r.OK {
		t.Error("ε=3 % > 2 % PLA : doit être non OK")
	}
	near(t, "y_max", r.MaxDeflectionMM, 0.02*100/3, 1e-9)
	// P = b h²/6 * E ε / L = 5*4/6*2750*0.03/10
	near(t, "P", r.BendingForceN, 5.0*4/6*2750*0.03/10, 1e-9)
	tn := math.Tan(30 * math.Pi / 180)
	if r.InsertionForceN == nil || r.InsertionForceUnbounded {
		t.Fatal("force d'insertion attendue")
	}
	near(t, "F insertion", *r.InsertionForceN, r.BendingForceN*(0.3+tn)/(1-0.3*tn), 1e-9)
	if r.Level != LevelHeuristic {
		t.Errorf("level %s", r.Level)
	}
	z, _ := SnapFit(SnapParams{Length: 10, Thickness: 2, Width: 5, Deflection: 1, Material: "PLA", Orientation: "z"})
	near(t, "ε_adm z", z.StrainAllowable, 0.01, 1e-12)
	ok, _ := SnapFit(SnapParams{Length: 20, Thickness: 1.5, Width: 5, Deflection: 1, Material: "PETG"})
	if !ok.OK {
		t.Errorf("PETG L=20 h=1.5 y=1 doit être OK (ε=%.4f)", ok.Value)
	}
	if _, err := SnapFit(SnapParams{Length: 0, Thickness: 2, Width: 5, Deflection: 1, Material: "PLA"}); !errors.Is(err, ErrInvalidInput) {
		t.Error("L=0 doit échouer")
	}
	if _, err := SnapFit(SnapParams{Length: 10, Thickness: 2, Width: 5, Deflection: 1, Material: "PLA", InsertAngle: 90}); !errors.Is(err, ErrInvalidInput) {
		t.Error("angle 90 doit échouer")
	}
	// Auto-blocage : JSON doit rester encodable.
	lock, err := SnapFit(SnapParams{Length: 10, Thickness: 2, Width: 5, Deflection: 1, Material: "PLA", InsertAngle: 75, Friction: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(lock)
	if err != nil {
		t.Errorf("json: %v", err)
	}
	if lock.InsertionForceN != nil || !lock.InsertionForceUnbounded || !strings.Contains(string(b), `"insertion_force_N":null`) {
		t.Errorf("force non bornée attendue (nulle) : %+v %s", lock.InsertionForceN, b)
	}
}

func TestShrinkage(t *testing.T) {
	s, err := Shrinkage("PLA")
	if err != nil {
		t.Fatal(err)
	}
	near(t, "s PLA", s.Value, 0.3, 1e-12)
	if s.Level != LevelHeuristic {
		t.Errorf("level %s", s.Level)
	}
	sc, err := ScaleForShrinkage("PLA", 100)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "100 mm", sc.Value, 100.30090, 1e-4)
	near(t, "facteur", sc.Factor, 1/0.997, 1e-12)
	if _, err := ScaleForShrinkage("PLA", 0); !errors.Is(err, ErrInvalidInput) {
		t.Error("dimension 0")
	}
	if _, err := Shrinkage("PVA"); !errors.Is(err, ErrInvalidInput) {
		t.Error("PVA retrait inconnu doit échouer")
	}
}

func TestWallCheck(t *testing.T) {
	r, err := WallCheck(1.2, 0.42)
	if err != nil {
		t.Fatal(err)
	}
	if r.Complete != 2 {
		t.Errorf("n = %d", r.Complete)
	}
	near(t, "reste", r.Remainder, 0.36, 1e-9)
	if !containsAny(r.Warnings, "Moteur classique") {
		t.Errorf("avertissement reste: %v", r.Warnings)
	}
	if r.ArachneLines != 3 { // round(2.857)=3
		t.Errorf("arachne %d", r.ArachneLines)
	}
	near(t, "largeur arachne", r.ArachneWidth, 0.4, 1e-9)
	if !r.ArachneOK {
		t.Error("0,4 mm dans la tolérance")
	}
	thin, _ := WallCheck(0.3, 0.42)
	if thin.Complete != 0 || !containsAny(thin.Warnings, "non imprimable") {
		t.Errorf("paroi fine: %+v", thin.Warnings)
	}
	exact, _ := WallCheck(0.84, 0.42)
	if exact.Complete != 2 || exact.Remainder != 0 {
		t.Errorf("exact: %d %v", exact.Complete, exact.Remainder)
	}
	for _, c := range [][2]float64{{0, 0.4}, {1, 0}, {-1, 0.4}, {1, 5}} {
		if _, err := WallCheck(c[0], c[1]); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%v doit échouer", c)
		}
	}
}

func TestFitAndHole(t *testing.T) {
	f, err := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical"})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "glissant", f.Value, 0.25, 1e-9)
	near(t, "par côté", f.PerSide, 0.125, 1e-9)
	near(t, "range", f.Range.Max-f.Range.Min, 0.1, 1e-9)
	f2, _ := FitClearance(FitParams{Fit: FitFree, Nozzle: 0.6, Orientation: "vertical"})
	near(t, "libre 0.6", f2.Value, 0.45, 1e-9)
	f3, _ := FitClearance(FitParams{Fit: FitSnug, Nozzle: 0.4, Orientation: "horizontal", Material: "PLA", Dimension: 100})
	near(t, "serré horiz PLA (retrait non ajouté)", f3.Value, 0.10+0.05, 1e-9)
	f4, _ := FitClearance(FitParams{Fit: FitPress, Nozzle: 0.4, Orientation: "vertical"})
	if f4.Value >= 0 {
		t.Error("press doit être une interférence")
	}
	if f.Level != LevelHeuristic || f.Validity == "" || len(f.Assumptions) == 0 {
		t.Error("contrat Result incomplet")
	}
	for _, bad := range []FitParams{
		{Fit: "x", Nozzle: 0.4, Orientation: "vertical"},
		{Fit: FitFree, Nozzle: 0, Orientation: "vertical"},
		{Fit: FitFree, Nozzle: 3, Orientation: "vertical"},
		{Fit: FitFree, Nozzle: 0.4, Orientation: "diag"},
		{Fit: FitFree, Nozzle: 0.4, Orientation: "vertical", Material: "nope", Dimension: 5},
	} {
		if _, err := FitClearance(bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v doit échouer", bad)
		}
	}

	h, err := HoleCompensation(HoleParams{Diameter: 10, Axis: "vertical", Nozzle: 0.4})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "trou vertical", h.Value, 10.15, 1e-9)
	h2, _ := HoleCompensation(HoleParams{Diameter: 10, Axis: "horizontal", Nozzle: 0.4})
	near(t, "trou horizontal", h2.Value, 10.25, 1e-9)
	h3, _ := HoleCompensation(HoleParams{Diameter: 2, Axis: "vertical", Nozzle: 0.4})
	if !containsAny(h3.Assumptions, "non sourcée") {
		t.Error("règle d<3 mm à marquer heuristique/non sourcée")
	}
	near(t, "petit trou", h3.Value, 2.2, 1e-9)
	h4, _ := HoleCompensation(HoleParams{Diameter: 100, Axis: "vertical", Nozzle: 0.4, Material: "PLA"})
	near(t, "retrait trou", h4.Value, 100+0.15+0.3, 1e-9)
	if _, err := HoleCompensation(HoleParams{Diameter: -1, Axis: "vertical", Nozzle: 0.4}); !errors.Is(err, ErrInvalidInput) {
		t.Error("d<0")
	}
	if _, err := HoleCompensation(HoleParams{Diameter: 5, Axis: "z?", Nozzle: 0.4}); !errors.Is(err, ErrInvalidInput) {
		t.Error("axe")
	}
}

func TestOverhangBridge(t *testing.T) {
	r, err := OverhangBridge(OverhangParams{AngleDeg: 30, BridgeLength: 10, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Supports || r.AngleStatus != OverhangOK {
		t.Errorf("30° ok: %+v", r)
	}
	r, _ = OverhangBridge(OverhangParams{AngleDeg: 50, Material: "PLA"})
	if r.AngleStatus != OverhangDegraded || r.Supports {
		t.Errorf("50° dégradé: %v", r.AngleStatus)
	}
	r, _ = OverhangBridge(OverhangParams{AngleDeg: 60, Material: "PLA"})
	if !r.Supports {
		t.Error("60° PLA : supports")
	}
	r, _ = OverhangBridge(OverhangParams{AngleDeg: 10, BridgeLength: 45, Material: "PLA"})
	if !r.Supports || r.BridgeStatus != OverhangSupports {
		t.Error("pont 45 mm PLA (max 30) : supports")
	}
	r, _ = OverhangBridge(OverhangParams{AngleDeg: 10, BridgeLength: 45, Material: "ABS"})
	if r.Supports == false {
		t.Error("pont 45 mm ABS (max 40) : supports")
	}
	if _, err := OverhangBridge(OverhangParams{AngleDeg: 95, Material: "PLA"}); !errors.Is(err, ErrInvalidInput) {
		t.Error("angle > 90")
	}
	if _, err := OverhangBridge(OverhangParams{AngleDeg: 10, BridgeLength: -1, Material: "PLA"}); !errors.Is(err, ErrInvalidInput) {
		t.Error("pont < 0")
	}
}

func TestScrewBoss(t *testing.T) {
	for _, sz := range ScrewSizes() {
		for _, k := range []ScrewKind{ScrewSelfTapping, ScrewHeatSet} {
			r, err := ScrewBoss(sz, k, "PETG")
			if err != nil {
				t.Fatalf("%s %s: %v", sz, k, err)
			}
			if r.Value <= 0 || r.BossOD <= r.Value {
				t.Errorf("%s %s: trou %v plot %v", sz, k, r.Value, r.BossOD)
			}
		}
	}
	if len(ScrewSizes()) != 7 {
		t.Error("M2,M2.5,M3,M4,M5,M6,M8")
	}
	m3, _ := ScrewBoss("m3", ScrewHeatSet, "")
	near(t, "insert M3", m3.Value, 4.0, 1e-9)
	near(t, "profondeur M3", m3.HoleDepth, 7.2, 1e-9)
	st, _ := ScrewBoss("M3", ScrewSelfTapping, "")
	near(t, "autotaraudeuse M3", st.Value, 2.5, 1e-9)
	for _, bad := range []struct {
		s string
		k ScrewKind
		m string
	}{{"M1", ScrewHeatSet, ""}, {"M10", ScrewHeatSet, ""}, {"M3", "colle", ""}, {"M3", ScrewHeatSet, "PVA"}, {"M3", ScrewHeatSet, "zzz"}} {
		if _, err := ScrewBoss(bad.s, bad.k, bad.m); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v doit échouer", bad)
		}
	}
	tpu, _ := ScrewBoss("M3", ScrewHeatSet, "TPU-95A")
	if !containsAny(tpu.Warnings, "TPU") {
		t.Error("avertissement TPU")
	}
}

func TestTorqueScrew(t *testing.T) {
	r, err := TorqueScrew(500, 3, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "couple", r.Value, 300, 1e-9)
	if _, err := TorqueScrew(0, 3, 0.2); !errors.Is(err, ErrInvalidInput) {
		t.Error("F=0")
	}
}

func TestResultJSONContract(t *testing.T) {
	r, _ := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PLA"})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"value", "unit", "assumptions", "validity", "level", "sources"} {
		if _, ok := m[k]; !ok {
			t.Errorf("clé JSON %q absente", k)
		}
	}
}

func containsAny(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestBeamRefusesLargeDeflection(t *testing.T) {
	// L=100, section 10x2 : flèche >> L/10.
	_, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 5, Length: 100,
		Section: Section{Shape: "rect", B: 10, H: 2}, Material: "PLA"})
	if !errors.Is(err, ErrNotApplicable) || !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("attendu ErrNotApplicable : %v", err)
	}
	// Juste sous le seuil : accepté.
	if _, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 10, Length: 100,
		Section: Section{Shape: "rect", B: 20, H: 10}, Material: "PLA"}); err != nil {
		t.Fatal(err)
	}
}

func TestIsElastomer(t *testing.T) {
	one := 300.0
	for _, m := range []*Material{
		{ID: "x", Family: "TPU"}, {ID: "tpe_90a", Family: "autre"}, {ID: "y", Family: "Flex"},
		{ID: "z", Family: "PU", Properties: map[string]Property{"elongation_xy": {Value: &one}}},
	} {
		if !isElastomer(m) {
			t.Errorf("%+v devrait être un élastomère", m)
		}
	}
	for _, id := range MaterialIDs() {
		m, _ := GetMaterial(id)
		if want := strings.HasPrefix(id, "TPU"); isElastomer(m) != want {
			t.Errorf("%s : isElastomer=%v, attendu %v", id, isElastomer(m), want)
		}
	}
	// Une poutre en TPU est refusée.
	_, err := BeamStress(BeamParams{Support: "cantilever", LoadType: "point", Load: 1, Length: 50,
		Section: Section{Shape: "rect", B: 20, H: 20}, Material: "TPU-95A"})
	if !errors.Is(err, ErrNotApplicable) {
		t.Errorf("poutre TPU : %v", err)
	}
}

func TestFitClearanceIgnoresShrinkageAndChecksDomain(t *testing.T) {
	a, _ := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical"})
	b, err := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Material: "PLA", Dimension: 100})
	if err != nil || a.Value != b.Value {
		t.Fatalf("le retrait ne doit pas modifier le jeu : %v %v %v", a.Value, b.Value, err)
	}
	if !containsAny(b.Assumptions, "rétrécissent ensemble") {
		t.Error("documentation du retrait manquante")
	}
	for _, d := range []float64{1, 150, math.NaN()} {
		if _, err := FitClearance(FitParams{Fit: FitSliding, Nozzle: 0.4, Orientation: "vertical", Dimension: d}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("dimension %v acceptée", d)
		}
	}
}

func TestHoleAndWallDomains(t *testing.T) {
	for _, d := range []float64{0.5, 150, 500} {
		if _, err := HoleCompensation(HoleParams{Diameter: d, Axis: "vertical", Nozzle: 0.4}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("diamètre %v accepté", d)
		}
	}
	if _, err := HoleCompensation(HoleParams{Diameter: 10, Axis: "vertical", Nozzle: 1.0}); !errors.Is(err, ErrInvalidInput) {
		t.Error("buse 1,0 hors domaine du trou")
	}
	for _, th := range []float64{0.1, 11} {
		if _, err := WallCheck(th, 0.4); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("épaisseur %v acceptée", th)
		}
	}
	// Retrait inconnu (PVA) : avertissement explicite.
	h, err := HoleCompensation(HoleParams{Diameter: 10, Axis: "vertical", Nozzle: 0.4, Material: "PVA"})
	if err != nil || !containsAny(h.Warnings, "inconnu") {
		t.Errorf("avertissement retrait inconnu : %v %v", h.Warnings, err)
	}
}

// Chaque valeur « measured » doit être retrouvable dans l'extrait exact de sa fiche
// (docs/research/materials-tds), et les réglages recommandés ne sont jamais « measured ».
func TestMeasuredValuesAreInTDSExtracts(t *testing.T) {
	d, err := Materials()
	if err != nil {
		t.Fatal(err)
	}
	numRe := regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`)
	cache := map[string][]float64{}
	extractNumbers := func(src string) []float64 {
		if v, ok := cache[src]; ok {
			return v
		}
		s, ok := d.Sources[src]
		if !ok || s.Extract == "" {
			t.Errorf("source %q sans fichier d'extrait", src)
			return nil
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(s.Extract)))
		if err != nil {
			t.Errorf("extrait %s illisible : %v", s.Extract, err)
			return nil
		}
		if !strings.Contains(string(raw), s.URL) || !strings.Contains(string(raw), s.SHA256) || s.SHA256 == "" {
			t.Errorf("%s : URL ou empreinte absentes de l'extrait", s.Extract)
		}
		var nums []float64
		for _, m := range numRe.FindAllString(string(raw), -1) {
			f, _ := strconv.ParseFloat(m, 64)
			nums = append(nums, f)
		}
		cache[src] = nums
		return nums
	}
	checked := 0
	for id, m := range d.Materials {
		for k, p := range m.Properties {
			if (k == "max_overhang_deg" || k == "max_bridge_mm") && p.Level == LevelMeasured {
				t.Errorf("%s.%s : une recommandation de réglage ne peut pas être « measured »", id, k)
			}
			if p.Level != LevelMeasured || p.Value == nil {
				continue
			}
			found := false
			for _, n := range extractNumbers(p.Source) {
				if math.Abs(n-*p.Value) < 1e-9 {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s.%s = %v (measured) introuvable dans l'extrait de %s", id, k, *p.Value, p.Source)
			}
			checked++
		}
	}
	if checked < 100 {
		t.Errorf("seulement %d valeurs mesurées vérifiées", checked)
	}
}

func TestOverhangBridgeResultIsHeuristic(t *testing.T) {
	r, err := OverhangBridge(OverhangParams{AngleDeg: 60, BridgeLength: 10, Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Level != LevelHeuristic {
		t.Errorf("level = %s, attendu heuristic (recommandation fabricant)", r.Level)
	}
}
