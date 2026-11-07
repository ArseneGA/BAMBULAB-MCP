package calibration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// sizes construit les mesures d'un modèle mesure = a·nominal + b (contour) et intérieur = a·nominal + c.
func sizes(a, b, c float64, nominals ...float64) []SizeMeasure {
	var out []SizeMeasure
	for _, n := range nominals {
		wall := 4.0
		if n > 30 {
			wall = 6.0
		}
		in := n - 2*wall
		v := a*n + b
		iv := a*in + c
		out = append(out, SizeMeasure{NominalMM: n, XMM: v, YMM: v + 0.02, ZMM: a * n, InnerNominalMM: in, InnerXMM: f(iv), InnerYMM: f(iv)})
	}
	return out
}

func TestShrinkSizesSeparatesScaleAndContourOffset(t *testing.T) {
	cal, notes, err := Compute(Measures{ShrinkSizes: sizes(0.997, 0.18, -0.16, 20, 80)})
	if err != nil {
		t.Fatal(err)
	}
	// mesure = 0,997·nominal + 0,18 : échelle 1/0,997, contour à retirer de 0,09 mm par côté (0,01 de dissymétrie X/Y négligé)
	near(t, *cal.ShrinkXY, 1/0.997, 5e-4, "échelle XY")
	near(t, *cal.ContourCompMM, -0.09, 0.01, "xy_contour_compensation")
	near(t, *cal.HoleCompMM, 0.08, 0.01, "xy_hole_compensation")
	near(t, *cal.ShrinkZ, 1/0.997, 1e-4, "échelle Z")
	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "xy_contour_compensation") || !strings.Contains(joined, "jamais appliqué") || !strings.Contains(joined, "Deux tailles") {
		t.Fatalf("notes : %v", notes)
	}
	// une pure échelle (b = 0) ne donne AUCUN décalage de contour : l'offset est bien séparé de l'échelle
	cal, _, err = Compute(Measures{ShrinkSizes: sizes(0.99, 0, 0, 20, 80)})
	if err != nil {
		t.Fatal(err)
	}
	near(t, *cal.ContourCompMM, -0.005, 0.01, "contour sans offset")
	near(t, *cal.ShrinkXY, 1/0.99, 1e-3, "échelle seule")
	// un pur décalage (a = 1) ne donne aucune échelle
	cal, _, _ = Compute(Measures{ShrinkSizes: sizes(1, 0.3, -0.2, 20, 80)})
	near(t, *cal.ShrinkXY, 1, 1e-3, "offset seul")
	near(t, *cal.ContourCompMM, -0.15, 0.01, "contour seul")
	// trois tailles : contrôle de linéarité mentionné par l'écart max, pas de note « Deux tailles »
	_, notes, err = Compute(Measures{ShrinkSizes: sizes(0.998, 0.1, -0.1, 20, 50, 80)})
	if err != nil || strings.Contains(strings.Join(notes, " "), "Deux tailles") {
		t.Fatalf("%v %v", notes, err)
	}
}

func TestShrinkSizesRejectsImplausible(t *testing.T) {
	bad := map[string][]SizeMeasure{
		"une seule taille":   sizes(1, 0, 0, 50),
		"écart de tailles":   sizes(1, 0, 0, 20, 30),
		"trop de tailles":    sizes(1, 0, 0, 20, 40, 60, 80, 100),
		"mesure à +20 %":     {{NominalMM: 20, XMM: 24, YMM: 24, ZMM: 20}, {NominalMM: 80, XMM: 80, YMM: 80, ZMM: 80}},
		"pente aberrante":    {{NominalMM: 20, XMM: 21, YMM: 21, ZMM: 20}, {NominalMM: 80, XMM: 70, YMM: 70, ZMM: 80}},
		"nominal hors plage": {{NominalMM: 5, XMM: 5, YMM: 5, ZMM: 5}, {NominalMM: 80, XMM: 80, YMM: 80, ZMM: 80}},
	}
	for name, ms := range bad {
		if _, _, err := Compute(Measures{ShrinkSizes: ms}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : accepté (%v)", name, err)
		}
	}
	// décalage de contour > 0,3 mm par côté : refusé
	if _, _, err := Compute(Measures{ShrinkSizes: sizes(1, 0.8, 0, 20, 80)}); !errors.Is(err, ErrInvalid) {
		t.Error("décalage de 0,4 mm par côté accepté")
	}
	// cote intérieure incomplète
	ms := sizes(1, 0, 0, 20, 80)
	ms[0].InnerYMM = nil
	if _, _, err := Compute(Measures{ShrinkSizes: ms}); !errors.Is(err, ErrInvalid) {
		t.Error("cote intérieure incomplète acceptée")
	}
}

func TestFlowSeriesRegression(t *testing.T) {
	var pts []FlowPoint
	for _, r := range []float64{0.94, 0.96, 0.98, 1.0, 1.02} {
		pts = append(pts, FlowPoint{Ratio: r, WallMM: 0.4 + (r-0.94)*1.5})
	}
	cal, notes, err := Compute(Measures{Flow: &FlowMeasure{Series: pts, TargetWallMM: 0.42}})
	if err != nil {
		t.Fatal(err)
	}
	near(t, *cal.BestFlowRatio, 0.94+0.02/1.5, 1e-3, "débit retenu")
	if !strings.Contains(strings.Join(notes, " "), "filament_flow_ratio") {
		t.Fatalf("%v", notes)
	}
	for name, m := range map[string]Measures{
		"hors borne":           {BestFlowRatio: f(1.3)},
		"exclusifs":            {BestFlowRatio: f(0.98), Flow: &FlowMeasure{Series: pts, TargetWallMM: 0.42}},
		"une seule pièce":      {Flow: &FlowMeasure{Series: pts[:1], TargetWallMM: 0.42}},
		"paroi décroissante":   {Flow: &FlowMeasure{Series: []FlowPoint{{0.94, 0.45}, {1.0, 0.40}}, TargetWallMM: 0.42}},
		"paroi implausible":    {Flow: &FlowMeasure{Series: []FlowPoint{{0.94, 1.5}, {1.0, 1.6}}, TargetWallMM: 0.42}},
		"débit déduit absurde": {Flow: &FlowMeasure{Series: []FlowPoint{{0.94, 0.40}, {1.0, 0.401}}, TargetWallMM: 0.45}},
		"rétraction 5 mm":      {BestRetractionMM: f(5)},
		"K négatif":            {BestPressureAdvance: f(-0.1)},
		"débit vol 80":         {BestMaxVolumetricSpeed: f(80)},
		"première couche ?":    {FirstLayer: &FirstLayerMeasure{Param: "z_offset", Value: 0.1}},
		"hauteur 1 mm":         {FirstLayer: &FirstLayerMeasure{Param: "height", Value: 1}},
	} {
		if _, _, err := Compute(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : accepté (%v)", name, err)
		}
	}
}

func TestExtrusionValuesAndProposals(t *testing.T) {
	cal, notes, err := Compute(Measures{BestFlowRatio: f(0.97), BestPressureAdvance: f(0.04), BestRetractionMM: f(0.6), BestMaxVolumetricSpeed: f(18),
		FirstLayer: &FirstLayerMeasure{Param: "speed", Value: 30}, BestTempC: f(215), ShrinkSizes: sizes(0.995, 0.2, -0.2, 20, 80)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(notes, " "), "16.0 mm³/s proposé") {
		t.Fatalf("marge de 90 %% : %v", notes)
	}
	p := ProposalsFor(&cal)
	byKey := map[string]Proposal{}
	for _, o := range p.Overrides {
		byKey[o.Key] = o
		if strings.HasSuffix(o.Key, "_gcode") {
			t.Fatalf("proposition G-code : %s", o.Key)
		}
	}
	want := map[string][2]string{
		"filament_flow_ratio": {"0.97", "filament"}, "retraction_length": {"0.6", "machine"},
		"filament_max_volumetric_speed": {"16", "filament"}, "initial_layer_speed": {"30", "process"}, "nozzle_temperature": {"215", "filament"},
		"xy_contour_compensation": {"-0.1", "process"}, "xy_hole_compensation": {"0.1", "process"},
	}
	for k, w := range want {
		o, ok := byKey[k]
		if !ok {
			t.Errorf("proposition %s absente (%v)", k, byKey)
			continue
		}
		if o.Target != w[1] || (k != "xy_contour_compensation" && k != "xy_hole_compensation" && o.Value != w[0]) {
			t.Errorf("%s = %+v, attendu %v", k, o, w)
		}
	}
	if _, ok := byKey["filament_retraction_length"]; ok {
		t.Error("filament_retraction_length n'est pas surchargeable (liste blanche) : ne doit pas être proposée")
	}
	if !strings.Contains(byKey["retraction_length"].Reason, "filament_retraction_length") {
		t.Error("la raison doit signaler que la clé filament prime")
	}
	// toute clé proposée ou autorisée dans une série est dans la liste blanche des surcharges (sinon modify_slicing_profile la refuserait)
	for _, o := range p.Overrides {
		if !slicer.IsOverridable(o.Key) {
			t.Errorf("proposition %s hors liste blanche", o.Key)
		}
	}
	for k := range seriesKeys {
		if !slicer.IsOverridable(k) {
			t.Errorf("clé de série %s hors liste blanche", k)
		}
	}
	// avance de pression : aucune clé de profil, réglage manuel ; échelle XY : réglage manuel aussi
	if _, ok := byKey["pressure_advance"]; ok || len(p.Manual) != 2 || !strings.Contains(p.Manual[0].Reason, "Flow Dynamics") {
		t.Fatalf("manuel : %+v", p.Manual)
	}
	if !strings.Contains(p.Note, "n'applique jamais") {
		t.Fatalf("note : %q", p.Note)
	}
	if ProposalsFor(nil) != nil || ProposalsFor(&engineering.Calibration{SlidesGapMM: f(0.3)}) != nil {
		t.Error("pas de proposition sans valeur de profil")
	}
}

func TestMergeKeepsExtrusionValuesPerField(t *testing.T) {
	a := &engineering.Calibration{BestFlowRatio: f(0.97), FirstLayer: &engineering.FirstLayerCal{HeightMM: f(0.2)}}
	b := &engineering.Calibration{BestRetractionMM: f(0.5), ContourCompMM: f(-0.05), FirstLayer: &engineering.FirstLayerCal{SpeedMMS: f(25)}}
	m := Merge(a, b)
	if *m.BestFlowRatio != 0.97 || *m.BestRetractionMM != 0.5 || *m.ContourCompMM != -0.05 || *m.FirstLayer.HeightMM != 0.2 || *m.FirstLayer.SpeedMMS != 25 {
		t.Fatalf("%+v", m)
	}
	if a.FirstLayer.SpeedMMS != nil {
		t.Fatal("la base a été modifiée")
	}
}

func TestSeriesBoundsRefuseGcodeAndOutOfRange(t *testing.T) {
	ok := []slicer.Override{{Key: "filament_flow_ratio", Value: "0.98"}, {Key: "spiral_mode", Value: "1"}, {Key: "retraction_length", Value: "0.8"}}
	if err := checkSeriesBounds(ok); err != nil {
		t.Fatal(err)
	}
	for _, o := range []slicer.Override{{Key: "machine_start_gcode", Value: "M104 S300"}, {Key: "filament_flow_ratio", Value: "1.4"},
		{Key: "retraction_length", Value: "9"}, {Key: "nozzle_temperature", Value: "400"}, {Key: "spiral_mode", Value: "0"},
		{Key: "filament_max_volumetric_speed", Value: "abc"}, {Key: "layer_gcode", Value: "1"}, {Key: "wall_loops", Value: "1"}} {
		if err := checkSeriesBounds([]slicer.Override{o}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v accepté (%v)", o, err)
		}
	}
}

// ---- service : séries, plage de température, repli des clés facultatives ----

type seriesCat struct {
	t    *testing.T
	reqs []map[string]any
}

func (c *seriesCat) RunWorker(_ context.Context, _ string, req map[string]any) (json.RawMessage, error) {
	c.reqs = append(c.reqs, req)
	out := req["out_dir"].(string)
	var pieces []map[string]any
	for _, ln := range []string{"0.4", "0.8"} {
		name := "calib_retract_" + ln
		for _, ext := range []string{".stl", ".step"} {
			if err := os.WriteFile(filepath.Join(out, name+ext), []byte("x"), 0o644); err != nil {
				c.t.Fatal(err)
			}
		}
		v := 0.4
		if ln == "0.8" {
			v = 0.8
		}
		pieces = append(pieces, map[string]any{"kind": "retraction", "name": name, "description": "d", "measure": []string{"m"},
			"files": map[string]string{"stl": filepath.Join(out, name+".stl"), "step": filepath.Join(out, name+".step")},
			"parts": []map[string]any{}, "bbox_mm": map[string]any{}, "info": map[string]any{}, "warnings": []string{},
			"series_value": v, "series_param": "retraction_length",
			"slicing_overrides":          []map[string]any{{"key": "retraction_length", "value": ln, "target": "machine"}},
			"slicing_overrides_optional": []map[string]any{{"key": "filament_retraction_length", "value": ln, "target": "filament"}}})
	}
	b, _ := json.Marshal(map[string]any{"pieces": pieces})
	return b, nil
}

func (c *seriesCat) RegisterGenerated(_ context.Context, _ string, m contracts.Model, _, _, _ string, _ []catalog.GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error) {
	m.ID = contracts.NewID("mdl")
	return m, contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID}, nil, nil
}

// pickyProfiles refuse la clé facultative (absente du profil résolu).
type pickyProfiles struct{ calls [][]slicer.Override }

func (p *pickyProfiles) ModifyProfile(_ context.Context, in goals.ModifyInput) (*goals.ModifyOutput, error) {
	p.calls = append(p.calls, in.Modifications)
	for _, m := range in.Modifications {
		if m.Key == "filament_retraction_length" {
			return nil, errors.New("clé inconnue dans les profils résolus")
		}
	}
	return &goals.ModifyOutput{Revision: "prof_" + in.Modifications[0].Value.(string)}, nil
}

type fakeRanges map[string][2]float64

func (r fakeRanges) NozzleTempRange(name string) (float64, float64, bool) {
	v, ok := r[name]
	return v[0], v[1], ok
}

func TestGenerateSeriesForwardsValuesAndFallsBackOnOptionalKey(t *testing.T) {
	cat := &seriesCat{t: t}
	pm := &pickyProfiles{}
	s := newSvc(t, cat, pm)
	out, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PLA"}, Kinds: []string{"retraction"},
		RetractionsMM: []float64{0.4, 0.8}, FlowRatios: []float64{0.97}, ShrinkSizes: []float64{20, 80}, VolumetricSpeed: []float64{12},
		PressureAdvance: []float64{0.03}, FirstLayerParam: "speed", FirstLayerVals: []float64{25, 35},
		Base: &goals.BaseSpec{Machine: "Bambu Lab P1S 0.4 nozzle", Filaments: []string{"Bambu PLA Basic @BBL P1S"}}})
	if err != nil {
		t.Fatal(err)
	}
	req := cat.reqs[0]
	for _, k := range []string{"retraction_lengths", "flow_ratios", "shrink_sizes", "volumetric_speeds", "pressure_advance_values", "first_layer_values"} {
		if _, ok := req[k]; !ok {
			t.Errorf("%s non transmis au worker : %v", k, req)
		}
	}
	if req["first_layer_param"] != "speed" {
		t.Errorf("first_layer_param : %v", req["first_layer_param"])
	}
	if len(out.Pieces) != 2 || out.Pieces[1].SeriesValue == nil || *out.Pieces[1].SeriesValue != 0.8 {
		t.Fatalf("%+v", out.Pieces)
	}
	// 1er essai avec la clé facultative (refusé), 2e sans : la révision principale existe, avec un avertissement
	if len(pm.calls) != 4 || len(pm.calls[0]) != 2 || len(pm.calls[1]) != 1 {
		t.Fatalf("appels : %v", pm.calls)
	}
	for _, p := range out.Pieces {
		if !strings.HasPrefix(p.ProfileRevision, "prof_") || len(p.OptionalOver) != 1 || !strings.Contains(strings.Join(p.Warnings, " "), "facultative ignorée") {
			t.Fatalf("%+v", p)
		}
	}
	if out.MeasuresTemplate["best_retraction_mm"] == nil {
		t.Fatalf("squelette : %v", out.MeasuresTemplate)
	}
}

func TestGenerateRefusesOutOfBoundsOverridesFromWorker(t *testing.T) {
	cat := &seriesCat{t: t}
	pm := &pickyProfiles{}
	s := newSvc(t, cat, pm)
	cat2 := &badCat{seriesCat: cat}
	s.d.Catalog = cat2
	_, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PLA"}, Kinds: []string{"retraction"},
		Base: &goals.BaseSpec{Machine: "m"}})
	if !errors.Is(err, ErrInvalid) || len(pm.calls) != 0 {
		t.Fatalf("surcharge hors bornes acceptée : %v (%d appels)", err, len(pm.calls))
	}
}

// badCat renvoie une surcharge G-code : elle ne doit jamais atteindre le profil.
type badCat struct{ *seriesCat }

func (c *badCat) RunWorker(ctx context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	raw, err := c.seriesCat.RunWorker(ctx, cmd, req)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(strings.ReplaceAll(string(raw), `"key":"retraction_length"`, `"key":"machine_start_gcode"`)), nil
}

func TestTemperatureRangeFromFilamentProfileThenMaterial(t *testing.T) {
	cat := &seriesCat{t: t}
	s := newSvc(t, cat, nil)
	s.d.Ranges = fakeRanges{"Bambu PETG HF @BBL P1S": {230, 270}}
	// 1. profil filament connu : sa plage prime
	tr := s.ResolveTempRange("PETG", &goals.BaseSpec{Filaments: []string{"Bambu PETG HF @BBL P1S"}})
	if tr.LowC != 230 || tr.HighC != 270 || !strings.Contains(tr.Source, "Bambu PETG HF") {
		t.Fatalf("%+v", tr)
	}
	// 2. profil inconnu : repli sur la table par famille (profils de base Bambu Studio)
	tr = s.ResolveTempRange("ABS", &goals.BaseSpec{Filaments: []string{"inconnu"}})
	if tr.LowC != 240 || tr.HighC != 280 || !strings.Contains(tr.Source, "Bambu Studio") {
		t.Fatalf("%+v", tr)
	}
	// 3. rien : plage historique, signalée
	tr = s.ResolveTempRange("matière-mystère", nil)
	if tr.LowC != 190 || tr.HighC != 230 || !strings.Contains(tr.Source, "générique") {
		t.Fatalf("%+v", tr)
	}
	// la série envoyée au worker suit la plage du profil, pas 190-230 °C
	if _, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PETG"}, Kinds: []string{"temperature"},
		Base: &goals.BaseSpec{Filaments: []string{"Bambu PETG HF @BBL P1S"}}}); err != nil {
		t.Fatal(err)
	}
	temps := cat.reqs[0]["temperatures"].([]float64)
	if temps[0] != 230 || temps[len(temps)-1] != 270 || len(temps) != 5 {
		t.Fatalf("températures : %v", temps)
	}
	// des températures fournies priment toujours
	if _, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PETG"}, Kinds: []string{"temperature"}, Temperatures: []float64{200, 210}}); err != nil {
		t.Fatal(err)
	}
	if got := cat.reqs[1]["temperatures"].([]float64); len(got) != 2 || got[0] != 200 {
		t.Fatalf("%v", got)
	}
}

func TestRecordAndResolveExposeProposals(t *testing.T) {
	s := newSvc(t, nil, nil)
	ctx := context.Background()
	k := Key{Printer: "P1S", NozzleMM: 0.4, Material: "PETG", Filament: "Bambu PETG HF vert"}
	o, err := s.Record(ctx, RecordInput{Key: k, Measures: Measures{BestFlowRatio: f(0.96), BestPressureAdvance: f(0.05), BestRetractionMM: f(0.6)}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Proposals == nil || len(o.Proposals.Overrides) != 2 || len(o.Proposals.Manual) != 1 {
		t.Fatalf("%+v", o.Proposals)
	}
	if !strings.Contains(strings.Join(o.Effects, " "), "PROPOSÉES") {
		t.Fatalf("effets : %v", o.Effects)
	}
	r, err := s.Resolve(ctx, k)
	if err != nil || r.Proposals == nil || len(r.Proposals.Overrides) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	// stocké par (imprimante, buse, bobine) : une autre bobine n'hérite pas du débit
	r, _ = s.Resolve(ctx, Key{Printer: "P1S", NozzleMM: 0.4, Material: "PETG", Filament: "Autre bobine"})
	if r.Calibration != nil {
		t.Fatalf("la valeur d'une bobine s'applique à une autre : %+v", r.Calibration)
	}
}
